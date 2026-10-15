package bot

import "testing"

func TestResolveWorkHoursWindow(t *testing.T) {
	cases := []struct {
		name     string
		startRaw string
		endRaw   string
		want     string
		wantOK   bool
	}{
		{
			name:     "支持 HH:MM 格式并补齐秒",
			startRaw: "09:00",
			endRaw:   "18:30",
			want:     "09:00:00-18:30:00",
			wantOK:   true,
		},
		{
			name:     "支持跨日班次",
			startRaw: "22:00:00",
			endRaw:   "06:00:00",
			want:     "22:00:00-06:00:00",
			wantOK:   true,
		},
		{
			name:     "非法开始时间返回失败",
			startRaw: "invalid",
			endRaw:   "18:00",
			want:     "",
			wantOK:   false,
		},
		{
			name:     "未配置时间返回失败",
			startRaw: "",
			endRaw:   "",
			want:     "",
			wantOK:   false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ResolveWorkHoursWindow(tc.startRaw, tc.endRaw)
			if ok != tc.wantOK {
				t.Fatalf("ok 不符合预期, want=%v got=%v", tc.wantOK, ok)
			}
			if got != tc.want {
				t.Fatalf("工作时间窗口不符合预期, want=%q got=%q", tc.want, got)
			}
		})
	}
}
