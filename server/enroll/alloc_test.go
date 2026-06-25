package main

import "testing"

// headscale 表格输出样例（默认 `headscale nodes list`）：首列 ID，含 Hostname 列。
const hsTable = `ID | Hostname | Name        | NodeKey | User  | IP addresses        | Ephemeral | Last seen | Online
1  | mac1     | mac1        | [abcd]  | admin | 100.64.0.1          | false     | now       | online
2  | mac3     | mac3        | [efgh]  | admin | 100.64.0.3          | false     | now       | offline
`

// headscale -o json 输出样例（节点 hostname 落在 given_name/name 字段）。
const hsJSON = `[
  {"id":"1","given_name":"mac1","name":"mac1.fleet.ts.net"},
  {"id":"2","given_name":"mac2","name":"mac2.fleet.ts.net"}
]`

func TestParseMaxMacIndex_Table(t *testing.T) {
	if got := parseMaxMacIndex(hsTable); got != 3 {
		t.Fatalf("表格输出应解析出 max=3，得 %d", got)
	}
}

func TestParseMaxMacIndex_JSON(t *testing.T) {
	if got := parseMaxMacIndex(hsJSON); got != 2 {
		t.Fatalf("JSON 输出应解析出 max=2，得 %d", got)
	}
}

func TestParseMaxMacIndex_Empty(t *testing.T) {
	// 无节点（仅表头或空）→ 0，下一个分配为 1
	if got := parseMaxMacIndex("ID | Hostname | Name\n"); got != 0 {
		t.Fatalf("空节点列表应得 0，得 %d", got)
	}
}

func TestComputeNext_HeadscaleMaxPlusOne(t *testing.T) {
	if got := computeNext(hsTable, nil, 0); got != 4 {
		t.Fatalf("max=3 → 下一个应为 4，得 %d", got)
	}
	if got := computeNext("", nil, 0); got != 1 {
		t.Fatalf("空 mesh → 第一台应为 1，得 %d", got)
	}
}

func TestComputeNext_NamesCoordination(t *testing.T) {
	// names.json 里已有 m5（显示名占用），即便 headscale 只到 mac3，也不能撞回 ≤5
	names := map[string]string{"m1": "客厅", "m5": "书房"}
	if got := computeNext(hsTable, names, 0); got != 6 {
		t.Fatalf("应纳入 names 占用 m5 → 下一个 6，得 %d", got)
	}
}

func TestComputeNext_FloorBridgesHeadscaleLag(t *testing.T) {
	// 关键并发场景：第一台已分配（floor=1）但尚未 `tailscale up`，headscale 仍为空。
	// 第二台到来时必须看 floor，拿到 2 而非又一个 1。
	if got := computeNext("", nil, 1); got != 2 {
		t.Fatalf("headscale 滞后时应靠 floor 得 2，得 %d", got)
	}
	// floor 落后于 headscale（进程重启后）时，以 headscale 为准
	if got := computeNext(hsTable, nil, 1); got != 4 {
		t.Fatalf("floor 落后时应以 headscale max=3 为准 → 4，得 %d", got)
	}
}
