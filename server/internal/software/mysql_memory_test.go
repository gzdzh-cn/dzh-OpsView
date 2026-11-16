package software

import "testing"

func TestMySQLMemoryBudgetPresetsAndOverflow(t *testing.T) {
	for _, version := range []string{"5.7.44", "8.0.46", "8.4.13"} {
		for _, budget := range []int{512, 1024, 2048, 4096, 8192} {
			values, err := MySQLMemoryPreset(budget)
			if err != nil {
				t.Fatal(err)
			}
			estimate, err := EstimateMySQLMemory(version, values)
			if err != nil || estimate.GlobalBytes < 256<<20 || estimate.PerConnectionBytes == 0 || estimate.EstimateBytes > uint64(budget)<<20 {
				t.Fatal(budget, estimate, err)
			}
		}
	}
	for _, values := range []map[string]string{{"max_connections": "9999999999"}, {"join_buffer_size": "999999999G", "max_connections": "1000000"}, {"max_connections": "1;shutdown"}} {
		if _, err := EstimateMySQLMemory("8.4.13", values); err == nil {
			t.Fatal("accepted invalid model", values)
		}
	}
}
