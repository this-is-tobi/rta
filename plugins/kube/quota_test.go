package main

import "testing"

func TestQuotaPercentValue(t *testing.T) {
	cases := []struct {
		used, hard string
		want       float64
		ok         bool
	}{
		{"2", "4", 0.5, true},
		{"500m", "1", 0.5, true},
		{"8Gi", "16Gi", 0.5, true},
		{"0", "4", 0, true},
		{"1", "0", 0, false},
		{"nope", "4", 0, false},
		{"1", "nope", 0, false},
	}
	for _, c := range cases {
		got, ok := quotaPercentValue(c.used, c.hard)
		if ok != c.ok || (ok && got != c.want) {
			t.Errorf("quotaPercentValue(%q, %q) = %v, %v; want %v, %v", c.used, c.hard, got, ok, c.want, c.ok)
		}
	}
}

func TestQuotaPercent(t *testing.T) {
	if got := quotaPercent("2", "4"); got != "50%" {
		t.Errorf("quotaPercent(2, 4) = %q, want 50%%", got)
	}
	if got := quotaPercent("1", "0"); got != "" {
		t.Errorf("quotaPercent(1, 0) = %q, want empty on a zero hard limit", got)
	}
}

func TestQuotaPressure(t *testing.T) {
	quotas := list[resourceQuotaItem]{Items: []resourceQuotaItem{
		{
			Metadata: meta{Namespace: "prod", Name: "compute"},
			Status: struct {
				Hard map[string]string `json:"hard"`
				Used map[string]string `json:"used"`
			}{
				Hard: map[string]string{"cpu": "4", "memory": "8Gi"},
				Used: map[string]string{"cpu": "3.6", "memory": "1Gi"},
			},
		},
		{
			Metadata: meta{Namespace: "staging", Name: "compute"},
			Status: struct {
				Hard map[string]string `json:"hard"`
				Used map[string]string `json:"used"`
			}{
				Hard: map[string]string{"cpu": "4"},
				Used: map[string]string{"cpu": "1"},
			},
		},
	}}

	got := quotaPressure(quotas, 0.8)
	if len(got) != 1 {
		t.Fatalf("quotaPressure = %v, want exactly the prod/compute cpu row", got)
	}
	want := "prod/compute cpu: 90%"
	if got[0] != want {
		t.Errorf("quotaPressure[0] = %q, want %q", got[0], want)
	}
}
