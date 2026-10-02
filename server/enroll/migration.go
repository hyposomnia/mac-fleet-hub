package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fleet-enroll/multiuser"
)

func resetAccountFromStdin(server *multiuser.Server, email string) error {
	reader := bufio.NewReader(os.Stdin)
	fmt.Fprintln(os.Stderr, "从标准输入读取新密码和确认密码两行；请通过受保护的输入传入。")
	password, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	confirm, err := reader.ReadString('\n')
	if err != nil {
		return err
	}
	password = strings.TrimSuffix(strings.TrimSuffix(password, "\n"), "\r")
	confirm = strings.TrimSuffix(strings.TrimSuffix(confirm, "\n"), "\r")
	if password != confirm {
		return errors.New("密码确认不一致")
	}
	return server.ResetPassword(email, password)
}

func migrateLegacy(server *multiuser.Server, options multiuser.Options, email string) error {
	users, err := server.Users()
	if err != nil {
		return err
	}
	var owner multiuser.User
	for _, user := range users {
		if strings.EqualFold(user.Email, strings.TrimSpace(email)) {
			owner = user
			break
		}
	}
	if owner.ID == 0 || owner.Status != "active" {
		return errors.New("请先注册并绑定迁移账号")
	}
	deviceIPs := strings.Fields(envOr("ENROLL_MAC_IPS", os.Getenv("MAC_IPS")))
	if len(deviceIPs) == 0 {
		return errors.New("迁移必须显式提供原有 MAC_IPS 顺序")
	}
	snapshot, ok := options.Network.(interface {
		Nodes(context.Context) ([]multiuser.Node, error)
	})
	if !ok {
		return errors.New("迁移需要 Headscale API 验证节点")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	nodes, err := snapshot.Nodes(ctx)
	if err != nil {
		return err
	}
	devices := []multiuser.Device{}
	seenNodes := map[string]bool{}
	names := loadNames()
	for index, ip := range deviceIPs {
		address, parseErr := netip.ParseAddr(ip)
		if parseErr != nil || !netip.MustParsePrefix("100.64.0.0/10").Contains(address) {
			return fmt.Errorf("第 %d 台旧设备的 mesh IP 不合法", index+1)
		}
		matches := []multiuser.Node{}
		for _, node := range nodes {
			if node.IP == ip {
				matches = append(matches, node)
			}
		}
		if len(matches) != 1 {
			return fmt.Errorf("第 %d 台旧设备无法唯一关联 Headscale 节点", index+1)
		}
		name := names["m"+strconv.Itoa(index+1)]
		if name == "" {
			name = fmt.Sprintf("Mac %d", index+1)
		}
		node := matches[0]
		if node.ID == "" || seenNodes[node.ID] {
			return errors.New("旧设备无法唯一关联有效 Headscale 节点")
		}
		seenNodes[node.ID] = true
		devices = append(devices, multiuser.Device{ID: "m" + strconv.Itoa(index+1), UserID: owner.ID, Index: index + 1, NodeID: node.ID, IP: node.IP, Name: name, Status: "active", Online: node.Online, LastSeen: node.LastSeen})
	}
	registered, err := server.Devices(0)
	if err != nil {
		return err
	}
	for _, expected := range devices {
		for _, device := range registered {
			if device.ID == expected.ID && (device.UserID != owner.ID || device.NodeID != expected.NodeID || device.IP != expected.IP || device.Status != "active") {
				return fmt.Errorf("旧设备 %s 的登记归属或节点已改变", expected.ID)
			}
		}
	}
	directory := filepath.Join(options.StateDir, "users", strconv.FormatInt(owner.ID, 10))
	var pending []struct {
		path  string
		value json.RawMessage
	}
	for _, source := range []struct{ path, name string }{{envOr("ENROLL_ACCESS_KEY_FILE", "/var/lib/fleet-enroll/access-key.json"), "access-key.json"}, {envOr("ENROLL_MESSAGE_JOBS_FILE", "/var/lib/fleet-enroll/message-jobs.json"), "message-jobs.json"}} {
		raw, readErr := os.ReadFile(source.path)
		if errors.Is(readErr, os.ErrNotExist) {
			continue
		}
		if readErr != nil {
			return readErr
		}
		if !json.Valid(raw) {
			return fmt.Errorf("旧 %s JSON 不合法", source.name)
		}
		value := raw
		if source.name == "message-jobs.json" {
			value, err = pinLegacyMessageJobs(raw, owner.ID, devices)
			if err != nil {
				return err
			}
		}
		target := filepath.Join(directory, source.name)
		if existing, readErr := os.ReadFile(target); readErr == nil {
			if equivalentMigrationJSON(existing, value) {
				continue
			}
			if !equivalentMigrationJSON(existing, raw) {
				return fmt.Errorf("目标 %s 已存在不同数据，停止迁移", source.name)
			}
		} else if !errors.Is(readErr, os.ErrNotExist) {
			return readErr
		}
		pending = append(pending, struct {
			path  string
			value json.RawMessage
		}{target, value})
	}
	preferences, _ := json.Marshal(loadSettings())
	if err = server.ImportLegacy(owner.Email, devices, preferences); err != nil {
		return err
	}
	allDevices, err := server.Devices(0)
	if err != nil {
		return err
	}
	users, err = server.Users()
	if err != nil {
		return err
	}
	if err = options.Network.Reconcile(ctx, allDevices, users); err != nil {
		return fmt.Errorf("设备已导入，网络策略未生效，请重试迁移: %w", err)
	}
	for _, expected := range devices {
		device, err := server.AuthorizedDevice(owner.ID, expected.ID)
		if err != nil || device.UserID != owner.ID || device.NodeID != expected.NodeID || device.IP != expected.IP {
			return fmt.Errorf("旧设备 %s 的已验证授权失效", expected.ID)
		}
	}
	for _, store := range pending {
		if err := writePrivateJSON(store.path, store.value); err != nil {
			return err
		}
	}
	return nil
}

func equivalentMigrationJSON(first, second []byte) bool {
	canonical := func(raw []byte) ([]byte, error) {
		if !json.Valid(raw) {
			return nil, errors.New("invalid JSON")
		}
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value interface{}
		if err := decoder.Decode(&value); err != nil {
			return nil, err
		}
		return json.Marshal(value)
	}
	firstValue, firstErr := canonical(first)
	secondValue, secondErr := canonical(second)
	return firstErr == nil && secondErr == nil && bytes.Equal(firstValue, secondValue)
}
