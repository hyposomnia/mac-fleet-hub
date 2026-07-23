package main

import "testing"

func TestSettingsNormalizeDefaults(t *testing.T) {
	// 全 0（缺省/空文件）→ 回退默认值
	s := dashSettings{}
	s.normalize()
	d := defaultSettings()
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
			"超上限钳到上限",
			dashSettings{DesktopMaxWindows: 999, DesktopScrollback: 999999, MobileMaxWindows: 999, MobileScrollback: 999999, AutoCloseMinutes: 99999, ChatCacheMaxSessions: 999},
			dashSettings{DesktopMaxWindows: 30, DesktopScrollback: 100000, MobileMaxWindows: 12, MobileScrollback: 100000, AutoCloseMinutes: 1440, ChatCacheMaxSessions: 20},
		},
		{
			"低于下限钳到下限",
			dashSettings{DesktopMaxWindows: -5, DesktopScrollback: 10, MobileMaxWindows: -1, MobileScrollback: 1, AutoCloseMinutes: -3, ChatCacheMaxSessions: -2},
			dashSettings{DesktopMaxWindows: 1, DesktopScrollback: 200, MobileMaxWindows: 1, MobileScrollback: 200, AutoCloseMinutes: 1, ChatCacheMaxSessions: 1},
		},
		{
			"合法值原样保留",
			dashSettings{DesktopMaxWindows: 10, DesktopScrollback: 5000, MobileMaxWindows: 4, MobileScrollback: 5000, AutoCloseMinutes: 30, ChatCacheMaxSessions: 6},
			dashSettings{DesktopMaxWindows: 10, DesktopScrollback: 5000, MobileMaxWindows: 4, MobileScrollback: 5000, AutoCloseMinutes: 30, ChatCacheMaxSessions: 6},
		},
		{"部分缺省只补缺项", dashSettings{DesktopMaxWindows: 6}, dashSettings{DesktopMaxWindows: 6, DesktopScrollback: 5000, MobileMaxWindows: 4, MobileScrollback: 5000, AutoCloseMinutes: 30, ChatCacheMaxSessions: 6}},
		{"自动关闭超上限钳到 24h", dashSettings{AutoCloseMinutes: 5000}, dashSettings{DesktopMaxWindows: 10, DesktopScrollback: 5000, MobileMaxWindows: 4, MobileScrollback: 5000, AutoCloseMinutes: 1440, ChatCacheMaxSessions: 6}},
	}
	for _, c := range cases {
		got := c.in
		got.normalize()
		if got != c.want {
			t.Errorf("%s: normalize(%+v)=%+v，期望 %+v", c.name, c.in, got, c.want)
		}
	}
}
