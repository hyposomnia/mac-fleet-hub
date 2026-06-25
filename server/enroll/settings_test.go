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
		{"超上限钳到上限", dashSettings{999, 999999, 999, 999999}, dashSettings{30, 100000, 12, 100000}},
		{"低于下限钳到下限", dashSettings{-5, 10, -1, 1}, dashSettings{1, 200, 1, 200}},
		{"合法值原样保留", dashSettings{10, 5000, 4, 5000}, dashSettings{10, 5000, 4, 5000}},
		{"部分缺省只补缺项", dashSettings{DesktopMaxWindows: 6}, dashSettings{6, 5000, 4, 5000}},
	}
	for _, c := range cases {
		got := c.in
		got.normalize()
		if got != c.want {
			t.Errorf("%s: normalize(%+v)=%+v，期望 %+v", c.name, c.in, got, c.want)
		}
	}
}
