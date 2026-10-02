package multiuser

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

type headscaleNetwork struct {
	baseURL      string
	apiKey       string
	gatewayIP    string
	servicePorts []int
	client       *http.Client
	issueLock    chan struct{}
	configErr    error
}

func NewHeadscale(baseURL, apiKey, gatewayIP string, servicePorts ...int) Network {
	network := &headscaleNetwork{
		apiKey: apiKey, gatewayIP: gatewayIP, issueLock: make(chan struct{}, 1),
		client: &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
	}
	if len(servicePorts) == 0 {
		servicePorts = []int{7681, 8080, 7682}
	}
	seenPorts := make(map[int]bool)
	for _, port := range servicePorts {
		if port < 1 || port > 65535 {
			network.configErr = errors.New("Headscale service ports must be between 1 and 65535")
			return network
		}
		if !seenPorts[port] {
			seenPorts[port] = true
			network.servicePorts = append(network.servicePorts, port)
		}
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil || endpoint.Hostname() == "" || endpoint.User != nil || endpoint.RawQuery != "" || endpoint.Fragment != "" || endpoint.Opaque != "" {
		network.configErr = errors.New("invalid Headscale endpoint")
		return network
	}
	loopback, _ := netip.ParseAddr(endpoint.Hostname())
	if endpoint.Scheme != "https" && !(endpoint.Scheme == "http" && loopback.IsLoopback()) {
		network.configErr = errors.New("Headscale endpoint requires HTTPS or an HTTP loopback address")
		return network
	}
	if strings.TrimSpace(apiKey) == "" || strings.ContainsAny(apiKey, "\r\n") {
		network.configErr = errors.New("invalid Headscale API key")
		return network
	}
	address, err := headscaleMeshIP(gatewayIP)
	if err != nil {
		network.configErr = fmt.Errorf("Headscale gateway: %w", err)
		return network
	}
	network.gatewayIP = address.String()
	network.baseURL = strings.TrimRight(endpoint.String(), "/")
	return network
}

type headscaleUser struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type headscaleKey struct {
	ID        string        `json:"id"`
	Key       string        `json:"key"`
	User      headscaleUser `json:"user"`
	Reusable  bool          `json:"reusable"`
	Ephemeral bool          `json:"ephemeral"`
	Used      bool          `json:"used"`
	ACLTags   []string      `json:"aclTags"`
}

func (network *headscaleNetwork) Issue(ctx context.Context, userID int64, index int) (Grant, error) {
	if network.configErr != nil {
		return Grant{}, network.configErr
	}
	if userID <= 0 || index <= 0 {
		return Grant{}, errors.New("invalid enrollment user or device index")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	select {
	case network.issueLock <- struct{}{}:
		defer func() { <-network.issueLock }()
	case <-ctx.Done():
		return Grant{}, ctx.Err()
	}
	name := "fleet-u" + strconv.FormatInt(userID, 10)
	user, err := network.findUser(ctx, name)
	if err != nil {
		return Grant{}, err
	}
	if user.ID == "" {
		var response struct {
			User headscaleUser `json:"user"`
		}
		createErr := network.request(ctx, http.MethodPost, "/api/v1/user", map[string]string{"name": name}, &response)
		if createErr != nil {
			user, err = network.findUser(ctx, name)
			if err != nil || user.ID == "" {
				return Grant{}, createErr
			}
		} else {
			user = response.User
		}
	}
	if !headscaleID(user.ID) || user.Name != name {
		return Grant{}, errors.New("Headscale returned an invalid enrollment user")
	}
	var response struct {
		PreAuthKey headscaleKey `json:"preAuthKey"`
	}
	body := struct {
		User       string `json:"user"`
		Reusable   bool   `json:"reusable"`
		Ephemeral  bool   `json:"ephemeral"`
		Expiration string `json:"expiration"`
	}{User: user.ID, Expiration: time.Now().UTC().Add(10 * time.Minute).Format(time.RFC3339Nano)}
	if err := network.request(ctx, http.MethodPost, "/api/v1/preauthkey", body, &response); err != nil {
		return Grant{}, err
	}
	key := response.PreAuthKey
	if !headscaleID(key.ID) || key.Key == "" || key.User.ID != user.ID || key.Reusable || key.Ephemeral || key.Used || len(key.ACLTags) != 0 {
		return Grant{}, errors.New("Headscale returned an invalid enrollment key")
	}
	return Grant{Key: key.Key, KeyID: key.ID}, nil
}

func (network *headscaleNetwork) findUser(ctx context.Context, name string) (headscaleUser, error) {
	var response struct {
		Users []headscaleUser `json:"users"`
	}
	if err := network.request(ctx, http.MethodGet, "/api/v1/user?name="+url.QueryEscape(name), nil, &response); err != nil {
		return headscaleUser{}, err
	}
	if len(response.Users) == 0 {
		return headscaleUser{}, nil
	}
	if len(response.Users) != 1 || response.Users[0].Name != name || !headscaleID(response.Users[0].ID) {
		return headscaleUser{}, errors.New("Headscale enrollment user is ambiguous or invalid")
	}
	return response.Users[0], nil
}

func (network *headscaleNetwork) Discover(ctx context.Context, grant Grant) (Node, error) {
	if !headscaleID(grant.KeyID) {
		return Node{}, errors.New("invalid enrollment key ID")
	}
	nodes, err := network.Nodes(ctx)
	if err != nil {
		return Node{}, err
	}
	var matched Node
	for _, node := range nodes {
		if node.KeyID == grant.KeyID {
			if matched.ID != "" {
				return Node{}, errors.New("multiple Headscale nodes use the enrollment key")
			}
			matched = node
		}
	}
	if matched.ID == "" {
		return Node{}, errors.New("Headscale enrollment node not found")
	}
	return matched, nil
}

func (network *headscaleNetwork) Nodes(ctx context.Context) ([]Node, error) {
	var response struct {
		Nodes []struct {
			ID          string        `json:"id"`
			IPAddresses []string      `json:"ipAddresses"`
			Name        string        `json:"name"`
			GivenName   string        `json:"givenName"`
			Online      bool          `json:"online"`
			LastSeen    *time.Time    `json:"lastSeen"`
			PreAuthKey  *headscaleKey `json:"preAuthKey"`
		} `json:"nodes"`
	}
	if err := network.request(ctx, http.MethodGet, "/api/v1/node", nil, &response); err != nil {
		return nil, err
	}
	nodes := make([]Node, 0, len(response.Nodes))
	seen := make(map[string]bool)
	for _, raw := range response.Nodes {
		if !headscaleID(raw.ID) || seen[raw.ID] {
			return nil, errors.New("Headscale returned invalid or duplicate node IDs")
		}
		seen[raw.ID] = true
		node := Node{ID: raw.ID, Name: raw.GivenName, Online: raw.Online}
		if node.Name == "" {
			node.Name = raw.Name
		}
		if raw.PreAuthKey != nil {
			if !headscaleID(raw.PreAuthKey.ID) {
				return nil, errors.New("Headscale returned an invalid node key ID")
			}
			node.KeyID = raw.PreAuthKey.ID
		}
		if raw.LastSeen != nil {
			node.LastSeen = raw.LastSeen.Unix()
		}
		for _, candidate := range raw.IPAddresses {
			address, err := headscaleMeshIP(candidate)
			if err != nil {
				return nil, fmt.Errorf("Headscale node %s: %w", raw.ID, err)
			}
			if node.IP == "" || address.Is4() {
				node.IP = address.String()
			}
		}
		if node.IP == "" {
			return nil, fmt.Errorf("Headscale node %s has no mesh IP", raw.ID)
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

type headscaleACL struct {
	Action string   `json:"action"`
	Src    []string `json:"src"`
	Dst    []string `json:"dst"`
}

func (network *headscaleNetwork) Reconcile(ctx context.Context, devices []Device, users []User) error {
	if network.configErr != nil {
		return network.configErr
	}
	activeUsers := make(map[int64]bool)
	seenUsers := make(map[int64]bool)
	for _, user := range users {
		if user.ID <= 0 || seenUsers[user.ID] {
			return errors.New("invalid or duplicate policy user")
		}
		seenUsers[user.ID] = true
		activeUsers[user.ID] = user.Status == "active"
	}
	byUser := make(map[int64][]string)
	seenIPs := make(map[string]bool)
	seenNodes := make(map[string]bool)
	var destinations []string
	for _, device := range devices {
		if device.Status != "active" || !activeUsers[device.UserID] || device.NodeID == "" {
			continue
		}
		address, err := headscaleMeshIP(device.IP)
		if err != nil {
			return fmt.Errorf("policy device %s: %w", device.ID, err)
		}
		ip := address.String()
		if !headscaleID(device.NodeID) || seenIPs[ip] || seenNodes[device.NodeID] || ip == network.gatewayIP {
			return errors.New("invalid or overlapping policy device identity")
		}
		seenIPs[ip], seenNodes[device.NodeID] = true, true
		byUser[device.UserID] = append(byUser[device.UserID], ip)
		for _, port := range network.servicePorts {
			destinations = append(destinations, ip+":"+strconv.Itoa(port))
		}
	}
	policy := struct {
		Hosts map[string]string `json:"hosts"`
		ACLs  []headscaleACL    `json:"acls"`
	}{Hosts: map[string]string{"fleet-gateway": network.gatewayIP}, ACLs: make([]headscaleACL, 0)}
	if len(destinations) > 0 {
		sort.Strings(destinations)
		policy.ACLs = append(policy.ACLs, headscaleACL{Action: "accept", Src: []string{network.gatewayIP}, Dst: destinations})
	}
	var userIDs []int64
	for userID := range byUser {
		userIDs = append(userIDs, userID)
	}
	sort.Slice(userIDs, func(first, second int) bool { return userIDs[first] < userIDs[second] })
	for _, userID := range userIDs {
		ips := byUser[userID]
		sort.Strings(ips)
		destinations := make([]string, 0, len(ips))
		for _, ip := range ips {
			destinations = append(destinations, ip+":*")
		}
		policy.ACLs = append(policy.ACLs, headscaleACL{Action: "accept", Src: ips, Dst: destinations})
	}
	encoded, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	return network.request(ctx, http.MethodPut, "/api/v1/policy", map[string]string{"policy": string(encoded)}, nil)
}

func (network *headscaleNetwork) Revoke(ctx context.Context, nodeID string) error {
	if !headscaleID(nodeID) {
		return errors.New("invalid Headscale node ID")
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	err := network.request(ctx, http.MethodPost, "/api/v1/node/"+nodeID+"/expire", nil, nil)
	var apiErr *headscaleHTTPError
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusNotFound {
		return nil
	}
	if errors.As(err, &apiErr) && apiErr.Status == http.StatusInternalServerError {
		nodes, inventoryErr := network.Nodes(ctx)
		if inventoryErr == nil {
			for _, node := range nodes {
				if node.ID == nodeID {
					return err
				}
			}
			return nil
		}
	}
	return err
}

type headscaleHTTPError struct {
	Method string
	Path   string
	Status int
}

func (failure *headscaleHTTPError) Error() string {
	return fmt.Sprintf("Headscale %s %s: HTTP %d", failure.Method, failure.Path, failure.Status)
}

func (network *headscaleNetwork) request(ctx context.Context, method, path string, body, output any) error {
	if network.configErr != nil {
		return network.configErr
	}
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return errors.New("cannot encode Headscale request")
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, network.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return errors.New("cannot create Headscale request")
	}
	request.Header.Set("Authorization", "Bearer "+network.apiKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := network.client.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("Headscale %s request: %w", method, ctx.Err())
		}
		return fmt.Errorf("Headscale %s %s: transport failure", method, path)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &headscaleHTTPError{Method: method, Path: path, Status: response.StatusCode}
	}
	const maxResponse = 4 << 20
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponse+1))
	if err != nil || len(data) > maxResponse {
		return fmt.Errorf("Headscale %s %s: unreadable or oversized response", method, path)
	}
	if output != nil {
		if err := json.Unmarshal(data, output); err != nil {
			return fmt.Errorf("Headscale %s %s: invalid JSON response", method, path)
		}
	}
	return nil
}

func headscaleID(value string) bool {
	parsed, err := strconv.ParseUint(value, 10, 64)
	return err == nil && parsed > 0 && strconv.FormatUint(parsed, 10) == value
}

func headscaleMeshIP(value string) (netip.Addr, error) {
	address, err := netip.ParseAddr(value)
	if err != nil || address.Zone() != "" || !(netip.MustParsePrefix("100.64.0.0/10").Contains(address) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(address)) {
		return netip.Addr{}, errors.New("invalid mesh IP")
	}
	return address, nil
}
