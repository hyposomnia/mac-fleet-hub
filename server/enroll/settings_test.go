package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettingsNormalizeDefaults(t *testing.T) {
	// 零值 idle 明确表示不回收；其余零值计数回退默认。
	s := dashSettings{}
	s.normalize()
	d := defaultSettings()
	d.AutoCloseMinutes = 0
	if s != d {
		t.Fatalf("空设置应回退默认 %+v，得到 %+v", d, s)
	}
}

func TestSettingsNormalizeClamp(t *testing.T) {
	cases := []struct {
		name string
		in   dashSettings
		want dashSettings
	}{
		{
			"连接目标无额外上限但回滚内存仍有边界",
			dashSettings{DesktopMaxWindows: 999, DesktopScrollback: 999999, MobileMaxWindows: 999, MobileScrollback: 999999, AutoCloseMinutes: 99999, ChatCacheMaxSessions: 999},
			dashSettings{DesktopMaxWindows: 999, DesktopScrollback: 100000, MobileMaxWindows: 999, MobileScrollback: 100000, AutoCloseMinutes: 99999, ChatCacheMaxSessions: 999},
		},
		{
			"低于下限钳到下限",
			dashSettings{DesktopMaxWindows: -5, DesktopScrollback: 10, MobileMaxWindows: -1, MobileScrollback: 1, AutoCloseMinutes: -3, ChatCacheMaxSessions: -2},
			dashSettings{DesktopMaxWindows: 1, DesktopScrollback: 200, MobileMaxWindows: 1, MobileScrollback: 200, AutoCloseMinutes: 0, ChatCacheMaxSessions: 1},
		},
		{
			"合法值原样保留",
			dashSettings{DesktopMaxWindows: 10, DesktopScrollback: 5000, MobileMaxWindows: 4, MobileScrollback: 5000, AutoCloseMinutes: 30, ChatCacheMaxSessions: 6},
			dashSettings{DesktopMaxWindows: 10, DesktopScrollback: 5000, MobileMaxWindows: 4, MobileScrollback: 5000, AutoCloseMinutes: 30, ChatCacheMaxSessions: 6},
		},
		{"部分缺省只补缺项", dashSettings{DesktopMaxWindows: 6}, dashSettings{DesktopMaxWindows: 6, DesktopScrollback: 5000, MobileMaxWindows: 4, MobileScrollback: 5000, AutoCloseMinutes: 0, ChatCacheMaxSessions: 6}},
		{"自动关闭支持超过24h", dashSettings{AutoCloseMinutes: 5000}, dashSettings{DesktopMaxWindows: 10, DesktopScrollback: 5000, MobileMaxWindows: 4, MobileScrollback: 5000, AutoCloseMinutes: 5000, ChatCacheMaxSessions: 6}},
	}
	for _, c := range cases {
		got := c.in
		got.normalize()
		if got != c.want {
			t.Errorf("%s: normalize(%+v)=%+v，期望 %+v", c.name, c.in, got, c.want)
		}
	}
}

func TestLoadSettingsDistinguishesMissingAndZeroIdle(t *testing.T) {
	old := settingsFile
	defer func() { settingsFile = old }()
	settingsFile = filepath.Join(t.TempDir(), "settings.json")
	if got := loadSettings(); got != defaultSettings() {
		t.Fatalf("missing file=%+v", got)
	}
	for _, test := range []struct {
		raw  string
		idle int
	}{{`{}`, 30}, {`{"desktopMaxWindows":50}`, 30}, {`{"autoCloseMinutes":0}`, 0}} {
		if err := os.WriteFile(settingsFile, []byte(test.raw), 0600); err != nil {
			t.Fatal(err)
		}
		if got := loadSettings(); got.AutoCloseMinutes != test.idle {
			t.Fatalf("%s idle=%d want%d", test.raw, got.AutoCloseMinutes, test.idle)
		}
	}
}
