package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type pairingGrant struct {
	deviceBinding
	AuthKey     string `json:"authKey"`
	LoginServer string `json:"loginServer"`
}

type pairingPending struct {
	Origin        string        `json:"origin"`
	ID            string        `json:"request_id"`
	Token         string        `json:"claim_token"`
	Code          string        `json:"code"`
	URL           string        `json:"verification_url"`
	Grant         *pairingGrant `json:"grant,omitempty"`
	Joined        bool          `json:"joined"`
	JoinAttempted bool          `json:"join_attempted"`
}

type loginOptions struct {
	Path           string
	Output         io.Writer
	NoOpen         bool
	ReplaceTailnet bool
	Reconfigure    bool
	Open           func(string) error
	Join           func(context.Context, pairingGrant, bool) error
	Setup          func(context.Context, deviceBinding) error
	Confirm        func(pairingGrant) error
	Browser        func(string, string)
	Authorize      func(context.Context, *http.Client, string, func(string, string)) (pairingPending, error)
}

func enrollmentCall(ctx context.Context, client *http.Client, origin, path string, input, output any) (int, error) {
	data, err := json.Marshal(input)
	if err != nil {
		return 0, err
	}
	request, err := http.NewRequestWithContext(ctx, "POST", origin+path, bytes.NewReader(data))
	if err != nil {
		return 0, err
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := client.Do(request)
	if err != nil {
		return 0, errors.New("无法连接服务，请检查网络并重试 login")
	}
	defer response.Body.Close()
	if response.StatusCode != 200 && response.StatusCode != 202 {
		return response.StatusCode, fmt.Errorf("配对请求被拒绝 (HTTP %d)，请在账号设备页检查授权", response.StatusCode)
	}
	if response.StatusCode == 200 && output != nil {
		err = json.NewDecoder(http.MaxBytesReader(nil, response.Body, 32<<10)).Decode(output)
	}
	return response.StatusCode, err
}

func pairingWait(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(2 * time.Second):
		return nil
	}
}

func pairDevice(ctx context.Context, origin string, options loginOptions) error {
	lock, err := lockDeviceState(options.Path)
	if err != nil {
		return err
	}
	defer lock.Close()
	origin, err = validateFleetOrigin(strings.TrimSpace(origin))
	if err != nil {
		return err
	}
	if options.Output == nil {
		options.Output = io.Discard
	}
	if binding, readErr := readDeviceBinding(options.Path); readErr == nil {
		if options.Authorize != nil {
			return errors.New("设备已有授权，请先解除关联再重新授权")
		}
		if binding.Origin != origin || binding.Locked {
			return errors.New("设备已绑定或已锁定，请先 logout 再更换账号/服务器")
		}
		if binding.Complete {
			if options.Reconfigure {
				if options.Setup == nil {
					return errors.New("没有配置本机安装程序")
				}
				if err := options.Setup(ctx, binding); err != nil {
					return err
				}
			}
			fmt.Fprintf(options.Output, "已关联 %s · %s；重启自动重连，无需重新授权。\n", binding.OwnerEmail, binding.DeviceID)
			return nil
		}
	} else if !os.IsNotExist(readErr) {
		return readErr
	}
	pendingPath := filepath.Join(filepath.Dir(options.Path), "pairing.json")
	var pending pairingPending
	client := deviceHTTPClient()
	defer client.CloseIdleConnections()
	if options.Authorize != nil {
		var previous pairingPending
		if previousErr := readPrivateJSON(pendingPath, &previous); previousErr == nil {
			if previous.Grant != nil {
				return errors.New("上次入网未完成，请先解除关联再重新授权")
			}
		} else if !os.IsNotExist(previousErr) {
			return previousErr
		}
		pending, err = options.Authorize(ctx, client, origin, options.Browser)
		if err != nil {
			return err
		}
		if err = writePrivateJSON(pendingPath, pending); err != nil {
			return err
		}
	} else if err = readPrivateJSON(pendingPath, &pending); os.IsNotExist(err) {
		name, _ := os.Hostname()
		_, err = enrollmentCall(ctx, client, origin, "/api/enrollment/start", map[string]string{"name": name}, &pending)
		if err != nil {
			return err
		}
		pending.Origin = origin
		if pending.ID == "" || pending.Token == "" || pending.Code == "" {
			return errors.New("服务器返回了不完整的配对请求")
		}
		if err = writePrivateJSON(pendingPath, pending); err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	if pending.Origin != origin {
		return errors.New("存在其他服务器的配对记录，请先 logout")
	}
	verification, err := url.Parse(pending.URL)
	base, _ := url.Parse(origin)
	if options.Authorize == nil && (err != nil || verification.Scheme != base.Scheme || verification.Host != base.Host || verification.User != nil || verification.Path != "/enroll/confirm") {
		return errors.New("服务器返回的确认链接不安全")
	}
	claim := map[string]string{"request_id": pending.ID, "claim_token": pending.Token}
	if options.Browser != nil && options.Authorize == nil {
		options.Browser(pending.URL, pending.Code)
	}
	if pending.Grant == nil {
		fmt.Fprintf(options.Output, "请在浏览器登录并确认关联：%s\n配对码：%s\n", pending.URL, pending.Code)
		if options.Authorize == nil && !options.NoOpen && options.Open != nil {
			if err = options.Open(pending.URL); err != nil {
				fmt.Fprintln(options.Output, "浏览器未自动打开，请手动打开上方链接。")
			}
		}
		for {
			var grant pairingGrant
			status, err := enrollmentCall(ctx, client, origin, "/api/enrollment/claim", claim, &grant)
			if err != nil {
				return err
			}
			if status == 200 {
				grant.Origin = origin
				if grant.DeviceID != "m"+grant.Index || !deviceIDPattern.MatchString(grant.DeviceID) || grant.OwnerEmail == "" || len(grant.DeviceToken) < 40 || len(grant.ProxyToken) < 40 || grant.DeviceToken == grant.ProxyToken || grant.AuthKey == "" {
					return errors.New("服务器返回的设备授权不完整")
				}
				if _, err = validateFleetOrigin(grant.LoginServer); err != nil {
					return err
				}
				for _, port := range []int{grant.AgentPort, grant.TerminalPort, grant.FilesPort} {
					if port < 1 || port > 65535 {
						return errors.New("服务器返回的服务端口不合法")
					}
				}
				pending.Grant = &grant
				if err = writePrivateJSON(pendingPath, pending); err != nil {
					return err
				}
				break
			}
			if err = pairingWait(ctx); err != nil {
				return err
			}
		}
	}
	grant := *pending.Grant
	if !pending.Joined && options.Confirm != nil {
		if err = options.Confirm(grant); err != nil {
			return err
		}
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	if err = writePrivateJSON(options.Path, grant.deviceBinding); err != nil {
		return err
	}
	if !pending.Joined {
		if pending.JoinAttempted {
			status, err := enrollmentCall(ctx, client, origin, "/api/enrollment/complete", claim, nil)
			if err != nil {
				return err
			}
			if status == 200 {
				pending.Joined = true
			}
		}
	}
	if !pending.Joined {
		if options.Join == nil {
			return errors.New("没有配置 mesh 接入程序")
		}
		pending.JoinAttempted = true
		if err = writePrivateJSON(pendingPath, pending); err != nil {
			return err
		}
		if err = options.Join(ctx, grant, options.ReplaceTailnet); err != nil {
			return err
		}
		pending.Joined = true
		if err = writePrivateJSON(pendingPath, pending); err != nil {
			return err
		}
	}
	for {
		status, err := enrollmentCall(ctx, client, origin, "/api/enrollment/complete", claim, nil)
		if err != nil {
			return err
		}
		if status == 200 {
			break
		}
		if err = pairingWait(ctx); err != nil {
			return err
		}
	}
	if options.Setup == nil {
		return errors.New("没有配置本机安装程序")
	}
	if err = options.Setup(ctx, grant.deviceBinding); err != nil {
		return err
	}
	grant.Complete = true
	if err = writePrivateJSON(options.Path, grant.deviceBinding); err != nil {
		return err
	}
	if err = os.Remove(pendingPath); err != nil {
		return err
	}
	fmt.Fprintf(options.Output, "已关联 %s · %s，打开 %s 查看设备。\n", grant.OwnerEmail, grant.DeviceID, origin)
	return nil
}

func readLoginOrigin(origin string, input *bufio.Reader, output io.Writer) (string, error) {
	if origin == "" {
		fmt.Fprint(output, "服务网页地址（例如 https://fleet.example.com）> ")
		value, err := input.ReadString('\n')
		if err != nil {
			return "", errors.New("未读取到服务网页地址，请在终端运行 fleet-agent login")
		}
		origin = value
	}
	return validateFleetOrigin(strings.TrimSpace(origin))
}

func confirmDeviceGrant(grant pairingGrant, input *bufio.Reader, output io.Writer) error {
	fmt.Fprintf(output, "浏览器已授权。请核对服务：%s\n归属账号：%s\n设备编号：%s\n确认在本机接入网络并安装服务？[y/N] > ", grant.Origin, grant.OwnerEmail, grant.DeviceID)
	value, err := input.ReadString('\n')
	if err != nil {
		return errors.New("未读取到确认，尚未接入网络；请在终端重试 login，或 logout 取消配对")
	}
	answer := strings.ToLower(strings.TrimSpace(value))
	if answer != "y" && answer != "yes" {
		return errors.New("已取消本机接入；可重试 login 继续，或 logout 取消配对")
	}
	return nil
}

func runDeviceLogin(args []string) int {
	flags := flag.NewFlagSet("login", flag.ContinueOnError)
	noOpen := flags.Bool("no-open", false, "仅输出确认链接")
	replace := flags.Bool("replace-tailnet", false, "明确允许切换已有 mesh")
	reconfigure := flags.Bool("configure", false, "重新安装本机服务，不改变设备归属")
	if err := flags.Parse(args); err != nil {
		return 2
	}
	if flags.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "用法: fleet-agent login [--no-open] [--replace-tailnet] [https://服务网页地址]")
		return 2
	}
	terminal, err := os.OpenFile("/dev/tty", os.O_RDWR, 0)
	var terminalInput io.Reader = os.Stdin
	if err == nil {
		defer terminal.Close()
		terminalInput = terminal
	}
	input := bufio.NewReader(terminalInput)
	origin, err := readLoginOrigin(flags.Arg(0), input, os.Stdout)
	if err != nil {
		return done(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	opener := func(link string) error {
		if runtime.GOOS != "darwin" {
			return errors.New("仅 macOS 自动打开浏览器")
		}
		return exec.Command("/usr/bin/open", link).Run()
	}
	confirmation := func(grant pairingGrant) error { return confirmDeviceGrant(grant, input, os.Stdout) }
	return done(pairDevice(ctx, origin, loginOptions{Path: bindingPath(), Output: os.Stdout, NoOpen: *noOpen, ReplaceTailnet: *replace, Reconfigure: *reconfigure, Open: opener, Confirm: confirmation, Join: joinDeviceMesh, Setup: setupBoundDevice}))
}

func joinDeviceMesh(ctx context.Context, grant pairingGrant, replace bool) error {
	home, _ := os.UserHomeDir()
	script := filepath.Join(home, ".macfleet", "support", "join-device.sh")
	keyFile, err := os.CreateTemp(filepath.Dir(bindingPath()), ".authkey-*")
	if err != nil {
		return err
	}
	defer os.Remove(keyFile.Name())
	if err = keyFile.Chmod(0600); err == nil {
		_, err = keyFile.WriteString(grant.AuthKey)
	}
	keyFile.Close()
	if err != nil {
		return err
	}
	command := exec.CommandContext(ctx, "/bin/bash", script)
	command.Env = append(os.Environ(), "LOGIN_SERVER="+grant.LoginServer, "MAC_INDEX="+grant.Index, "FLEET_AUTHKEY_FILE="+keyFile.Name(), fmt.Sprintf("FLEET_REPLACE_TAILNET=%t", replace))
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	return command.Run()
}

func setupBoundDevice(ctx context.Context, binding deviceBinding) error {
	home, _ := os.UserHomeDir()
	command := exec.CommandContext(ctx, "/bin/bash", filepath.Join(home, ".macfleet", "support", "setup-mac.sh"))
	command.Env = append(os.Environ(), "MAC_INDEX="+binding.Index, "FLEET_BINDING_FILE="+bindingPath(), "FLEET_UPDATE_BASE="+binding.Origin+"/enroll/dist", fmt.Sprintf("AGENT_PORT=%d", binding.AgentPort), fmt.Sprintf("TTYD_PORT=%d", binding.TerminalPort), fmt.Sprintf("FB_PORT=%d", binding.FilesPort))
	command.Stdout = os.Stdout
	command.Stderr = os.Stderr
	command.Stdin = os.Stdin
	return command.Run()
}

func deviceIdentityStatus() string {
	binding, err := readDeviceBinding(bindingPath())
	if err != nil {
		return "设备授权：未关联（fleet-agent login https://服务网页地址）"
	}
	state := "已关联"
	if binding.Locked {
		state = "本地已锁定，需重试 logout"
	} else if !binding.Complete {
		state = "安装待完成，请重试 login"
	}
	return fmt.Sprintf("设备授权：%s · %s · %s · %s", state, binding.OwnerEmail, binding.DeviceID, binding.Origin)
}

func logoutDevice(ctx context.Context, path string) error {
	lock, err := lockDeviceState(path)
	if err != nil {
		return err
	}
	defer lock.Close()
	binding, err := readDeviceBinding(path)
	if os.IsNotExist(err) {
		pendingPath := filepath.Join(filepath.Dir(path), "pairing.json")
		var pending pairingPending
		if err = readPrivateJSON(pendingPath, &pending); os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if pending.Grant == nil {
			return os.Remove(pendingPath)
		}
		binding = pending.Grant.deviceBinding
	} else if err != nil {
		return err
	}
	binding.Locked = true
	if err = writePrivateJSON(path, binding); err != nil {
		return err
	}
	client := deviceHTTPClient()
	defer client.CloseIdleConnections()
	request, _ := http.NewRequestWithContext(ctx, "DELETE", binding.Origin+"/api/device/binding", nil)
	request.Header.Set("Authorization", "Bearer "+binding.DeviceToken)
	response, err := client.Do(request)
	if err != nil {
		return errors.New("本地访问已锁定，服务不可达；保留凭据，请稍后重试 logout")
	}
	response.Body.Close()
	if response.StatusCode != 200 && response.StatusCode != 410 {
		return fmt.Errorf("本地已锁定，远端撤销待重试 (HTTP %d)", response.StatusCode)
	}
	if err = os.Remove(path); err != nil {
		return err
	}
	if err = os.Remove(filepath.Join(filepath.Dir(path), "pairing.json")); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}
