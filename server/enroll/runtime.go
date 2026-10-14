package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"fleet-enroll/multiuser"
)

func serverOptionsFromEnv() (multiuser.Options, error) {
	options := multiuser.Options{Origin: os.Getenv("FLEET_ORIGIN"), StateDir: envOr("FLEET_STATE_DIR", "/var/lib/fleet-enroll"), StaticDir: envOr("FLEET_STATIC_DIR", "/var/www/fleet"), KeyFile: os.Getenv("FLEET_KEY_FILE"), LoginServer: loginServer, AgentPort: envInt("ENROLL_AGENT_PORT", 7682), TerminalPort: envInt("ENROLL_TTYD_PORT", envInt("TTYD_PORT", 7681)), FilesPort: envInt("ENROLL_FB_PORT", envInt("FB_PORT", 8080))}
	if options.Origin == "" {
		return options, errors.New("必须配置 FLEET_ORIGIN，服务仅运行统一多用户入口")
	}
	if err := authPolicyFromEnv(&options); err != nil {
		return options, err
	}
	options.MeshProxy = os.Getenv("FLEET_MESH_PROXY")
	if _, err := multiuser.NewDeviceTransport(options.MeshProxy); err != nil {
		return options, err
	}
	if base := os.Getenv("FLEET_HEADSCALE_URL"); base != "" {
		keyFile := os.Getenv("FLEET_HEADSCALE_API_KEY_FILE")
		key, err := os.ReadFile(keyFile)
		if err != nil {
			return options, fmt.Errorf("读取 Headscale API key: %w", err)
		}
		gatewayIP := os.Getenv("FLEET_GATEWAY_IP")
		if gatewayIP == "" {
			return options, errors.New("缺少 FLEET_GATEWAY_IP")
		}
		options.Network = multiuser.NewHeadscale(base, strings.TrimSpace(string(key)), gatewayIP, options.TerminalPort, options.FilesPort, options.AgentPort)
	}
	return options, nil
}

// Explicit invalid policies fail startup instead of silently relaxing authentication.
func authPolicyFromEnv(options *multiuser.Options) error {
	graceSeconds := 120
	if raw := os.Getenv("FLEET_DEVICE_OUTAGE_GRACE_SECONDS"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 0 || value > 600 {
			return errors.New("invalid FLEET_DEVICE_OUTAGE_GRACE_SECONDS (expected 0..600)")
		}
		graceSeconds = value
	}
	grace := time.Duration(graceSeconds) * time.Second
	options.DeviceOutageGrace = &grace

	for _, item := range []struct {
		name              string
		target            *time.Duration
		fallback, minimum time.Duration
	}{
		{"FLEET_SESSION_LIFETIME", &options.SessionLifetime, 30 * 24 * time.Hour, time.Minute},
		{"FLEET_AUTH_WINDOW", &options.AuthWindow, 15 * time.Minute, time.Second},
	} {
		value := item.fallback
		if raw := os.Getenv(item.name); raw != "" {
			parsed, err := time.ParseDuration(raw)
			if err != nil || parsed < item.minimum {
				return fmt.Errorf("invalid %s", item.name)
			}
			value = parsed
		}
		*item.target = value
	}
	for _, item := range []struct {
		name     string
		target   *int
		fallback int
	}{
		{"FLEET_AUTH_ATTEMPTS", &options.AuthAttempts, 8}, {"FLEET_AUTH_CONCURRENCY", &options.AuthConcurrency, 4},
	} {
		value := item.fallback
		if raw := os.Getenv(item.name); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 {
				return fmt.Errorf("invalid %s", item.name)
			}
			value = parsed
		}
		*item.target = value
	}
	options.SessionSliding = true
	if raw := os.Getenv("FLEET_SESSION_SLIDING"); raw != "" {
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return errors.New("invalid FLEET_SESSION_SLIDING")
		}
		options.SessionSliding = value
	}
	if raw, present := os.LookupEnv("FLEET_TRUSTED_PROXIES"); present {
		options.TrustedProxies = []string{}
		for _, part := range strings.Split(raw, ",") {
			cidr := strings.TrimSpace(part)
			if cidr == "" {
				continue
			}
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return errors.New("invalid FLEET_TRUSTED_PROXIES")
			}
			options.TrustedProxies = append(options.TrustedProxies, cidr)
		}
	}
	return nil
}

func runUnifiedServer() error {
	options, err := serverOptionsFromEnv()
	if err != nil {
		return err
	}
	applications := newUserApplications(options.StateDir)
	applications.meshProxy = options.MeshProxy
	options.Application = applications.Handler
	options.PublicAPI = applications
	server, err := multiuser.New(options)
	if err != nil {
		return err
	}
	defer server.Close()
	applications.server = server
	defer applications.Close()
	if len(os.Args) > 1 {
		if len(os.Args) != 3 {
			return errors.New("用法: fleet-enroll admin|migrate|reset-password <email>")
		}
		switch os.Args[1] {
		case "admin":
			return server.SetAdmin(os.Args[2])
		case "migrate":
			return migrateLegacy(server, options, os.Args[2])
		case "reset-password":
			return resetAccountFromStdin(server, os.Args[2])
		default:
			return errors.New("未知子命令")
		}
	}
	if err := applications.Start(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		for ctx.Err() == nil {
			if err := server.Run(ctx); err != nil && ctx.Err() == nil {
				log.Printf("Headscale 状态/策略同步失败: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
			}
		}
	}()
	httpServer := &http.Server{Addr: listen, Handler: server, ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 90 * time.Second, MaxHeaderBytes: 1 << 20}
	go func() {
		<-ctx.Done()
		shutdown, close := context.WithTimeout(context.Background(), 5*time.Second)
		defer close()
		httpServer.Shutdown(shutdown)
	}()
	log.Printf("fleet-server unified multi-user listening %s origin=%s", listen, options.Origin)
	err = httpServer.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}
