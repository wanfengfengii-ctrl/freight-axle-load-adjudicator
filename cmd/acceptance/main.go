// Command acceptance 是一次性黑盒验收程序：等待服务就绪后，对运行中的 API
// 执行 1800/1801 毫米临界两侧、非法输入 422 与可重复性检查，全部通过才以 0 退出。
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const overallTimeout = 45 * time.Second

func main() {
	base := envOr("BASE_URL", "http://localhost:8080")
	ctx, cancel := context.WithTimeout(context.Background(), overallTimeout)
	defer cancel()

	failures := 0
	check := func(name string, fn func() error) {
		if err := fn(); err != nil {
			fmt.Printf("[FAIL] %s: %v\n", name, err)
			failures++
			return
		}
		fmt.Printf("[PASS] %s\n", name)
	}

	fmt.Printf("等待 API 就绪: %s/healthz ...\n", base)
	if err := waitReady(ctx, base+"/healthz"); err != nil {
		fmt.Printf("服务在 %s 内未就绪: %v\n", overallTimeout, err)
		os.Exit(1)
	}
	fmt.Println("服务已就绪")

	client := &http.Client{Timeout: 10 * time.Second}

	// 1800 毫米：<=1800 同组，双轴组 19000 > 18000，组超限、整车不超限。
	check("轴距 1800mm 两轴同组且双轴组超限", func() error {
		code, body, err := postJSON(ctx, client, base+"/api/v1/verify",
			map[string]any{"axle_loads_kg": []int{9500, 9500}, "axle_spacings_mm": []int{1800}})
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return fmt.Errorf("期望 200，实际 %d，响应 %s", code, body)
		}
		var got struct {
			Groups []struct {
				Index     int  `json:"index"`
				StartAxle int  `json:"start_axle"`
				EndAxle   int  `json:"end_axle"`
				AxleCount int  `json:"axle_count"`
				LoadKg    int  `json:"load_kg"`
				LimitKg   int  `json:"limit_kg"`
				OverLimit bool `json:"over_limit"`
			} `json:"groups"`
			Vehicle struct {
				LoadKg    int  `json:"load_kg"`
				LimitKg   int  `json:"limit_kg"`
				OverLimit bool `json:"over_limit"`
			} `json:"vehicle"`
			Violations []map[string]any `json:"violations"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			return err
		}
		if len(got.Groups) != 1 {
			return fmt.Errorf("期望 1 个轴组，实际 %d", len(got.Groups))
		}
		g := got.Groups[0]
		if g != (struct {
			Index     int  `json:"index"`
			StartAxle int  `json:"start_axle"`
			EndAxle   int  `json:"end_axle"`
			AxleCount int  `json:"axle_count"`
			LoadKg    int  `json:"load_kg"`
			LimitKg   int  `json:"limit_kg"`
			OverLimit bool `json:"over_limit"`
		}{1, 1, 2, 2, 19000, 18000, true}) {
			return fmt.Errorf("轴组裁决不符: %+v", g)
		}
		if got.Vehicle.LoadKg != 19000 || got.Vehicle.LimitKg != 49000 || got.Vehicle.OverLimit {
			return fmt.Errorf("整车裁决不符: %+v", got.Vehicle)
		}
		if len(got.Violations) != 1 || got.Violations[0]["scope"] != "group" ||
			int(got.Violations[0]["index"].(float64)) != 1 {
			return fmt.Errorf("超限清单不符: %+v", got.Violations)
		}
		return nil
	})

	// 1801 毫米：>1800 开启下一组，两个单轴组各 9500 <= 10000，全部合规。
	check("轴距 1801mm 拆成两个合规单轴组", func() error {
		code, body, err := postJSON(ctx, client, base+"/api/v1/verify",
			map[string]any{"axle_loads_kg": []int{9500, 9500}, "axle_spacings_mm": []int{1801}})
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return fmt.Errorf("期望 200，实际 %d，响应 %s", code, body)
		}
		var got struct {
			Groups []struct {
				StartAxle int  `json:"start_axle"`
				EndAxle   int  `json:"end_axle"`
				AxleCount int  `json:"axle_count"`
				LoadKg    int  `json:"load_kg"`
				LimitKg   int  `json:"limit_kg"`
				OverLimit bool `json:"over_limit"`
			} `json:"groups"`
			Violations []any `json:"violations"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			return err
		}
		if len(got.Groups) != 2 {
			return fmt.Errorf("期望 2 个轴组，实际 %d", len(got.Groups))
		}
		for i, g := range got.Groups {
			if g.AxleCount != 1 || g.LoadKg != 9500 || g.LimitKg != 10000 || g.OverLimit ||
				g.StartAxle != i+1 || g.EndAxle != i+1 {
				return fmt.Errorf("第 %d 组裁决不符: %+v", i+1, g)
			}
		}
		if len(got.Violations) != 0 {
			return fmt.Errorf("期望无超限，实际 %+v", got.Violations)
		}
		return nil
	})

	// 四轴及以上轴组非法：统一 422 且不得夹带任何部分结果。
	check("形成四轴组时返回 422 且无部分结果", func() error {
		code, body, err := postJSON(ctx, client, base+"/api/v1/verify",
			map[string]any{
				"axle_loads_kg":    []int{1, 1, 1, 1},
				"axle_spacings_mm": []int{1800, 1800, 1800},
			})
		if err != nil {
			return err
		}
		if code != http.StatusUnprocessableEntity {
			return fmt.Errorf("期望 422，实际 %d，响应 %s", code, body)
		}
		if bytes.Contains(body, []byte("groups")) || bytes.Contains(body, []byte("vehicle")) {
			return fmt.Errorf("422 响应夹带了裁决结果: %s", body)
		}
		var errResp struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &errResp); err != nil || errResp.Error == "" {
			return fmt.Errorf("422 响应缺少 error 字段: %s", body)
		}
		return nil
	})

	// 合法对象后多写右方括号（及其他尾随垃圾）：必须 422 拒绝，不得给出裁决。
	check("合法对象后多余右括号时返回 422", func() error {
		for _, suffix := range []string{"]", "}", "1", ",\n"} {
			raw := []byte(`{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1800]}` + suffix)
			req, _ := http.NewRequestWithContext(ctx, http.MethodPost, base+"/api/v1/verify", bytes.NewReader(raw))
			req.Header.Set("Content-Type", "application/json")
			resp, err := client.Do(req)
			if err != nil {
				return fmt.Errorf("后缀 %q 请求失败: %w", suffix, err)
			}
			body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
			resp.Body.Close()
			if resp.StatusCode != http.StatusUnprocessableEntity {
				return fmt.Errorf("后缀 %q 期望 422，实际 %d，响应 %s", suffix, resp.StatusCode, body)
			}
			if bytes.Contains(body, []byte("groups")) {
				return fmt.Errorf("后缀 %q 的 422 夹带了裁决结果: %s", suffix, body)
			}
		}
		return nil
	})

	// 同一请求两次，原始响应必须逐字节一致（可重复、唯一执法结论）。
	check("相同输入两次响应逐字节一致", func() error {
		req := map[string]any{
			"axle_loads_kg":    []int{20000, 20000, 10000},
			"axle_spacings_mm": []int{1801, 1801},
		}
		_, first, err := postJSON(ctx, client, base+"/api/v1/verify", req)
		if err != nil {
			return err
		}
		_, second, err := postJSON(ctx, client, base+"/api/v1/verify", req)
		if err != nil {
			return err
		}
		if !bytes.Equal(first, second) {
			return fmt.Errorf("响应不一致:\n%s\n%s", first, second)
		}
		return nil
	})

	// 地磅校准：同一载荷未校准时双轴组 18400 > 18000 超限；
	// 携带地磅 18000 校准为 [9000,9000] 后等于限值合规，结论翻转。
	check("地磅校准后超限结论翻转", func() error {
		code, body, err := postJSON(ctx, client, base+"/api/v1/verify",
			map[string]any{"axle_loads_kg": []int{9200, 9200}, "axle_spacings_mm": []int{1800}})
		if err != nil {
			return err
		}
		if code != http.StatusOK || !bytes.Contains(body, []byte(`"over_limit":true`)) {
			return fmt.Errorf("未校准时期望组超限，实际 %d，响应 %s", code, body)
		}

		code, body, err = postJSON(ctx, client, base+"/api/v1/verify",
			map[string]any{
				"axle_loads_kg":    []int{9200, 9200},
				"axle_spacings_mm": []int{1800},
				"scale_weight_kg":  18000,
			})
		if err != nil {
			return err
		}
		if code != http.StatusOK {
			return fmt.Errorf("期望 200，实际 %d，响应 %s", code, body)
		}
		var got struct {
			Groups []struct {
				LoadKg    int  `json:"load_kg"`
				OverLimit bool `json:"over_limit"`
			} `json:"groups"`
			Vehicle struct {
				LoadKg    int  `json:"load_kg"`
				OverLimit bool `json:"over_limit"`
			} `json:"vehicle"`
			Violations  []any `json:"violations"`
			Calibration struct {
				ScaleWeightKg     int   `json:"scale_weight_kg"`
				OriginalTotalKg   int   `json:"original_total_kg"`
				DifferenceKg      int   `json:"difference_kg"`
				CalibratedLoadsKg []int `json:"calibrated_loads_kg"`
			} `json:"calibration"`
		}
		if err := json.Unmarshal(body, &got); err != nil {
			return err
		}
		if len(got.Groups) != 1 || got.Groups[0].LoadKg != 18000 || got.Groups[0].OverLimit {
			return fmt.Errorf("校准后轴组裁决不符: %+v", got.Groups)
		}
		if got.Vehicle.LoadKg != 18000 || got.Vehicle.OverLimit || len(got.Violations) != 0 {
			return fmt.Errorf("校准后整车裁决不符: %+v，超限清单 %+v", got.Vehicle, got.Violations)
		}
		cal := got.Calibration
		if cal.ScaleWeightKg != 18000 || cal.OriginalTotalKg != 18400 || cal.DifferenceKg != -400 ||
			len(cal.CalibratedLoadsKg) != 2 || cal.CalibratedLoadsKg[0] != 9000 || cal.CalibratedLoadsKg[1] != 9000 {
			return fmt.Errorf("校准信息不符: %+v", cal)
		}
		return nil
	})

	// 地磅重量与轴载荷合计偏差超过 5%：422 且不得夹带任何部分结果。
	check("地磅偏差超过 5% 返回 422 且无部分结果", func() error {
		code, body, err := postJSON(ctx, client, base+"/api/v1/verify",
			map[string]any{
				"axle_loads_kg":    []int{10000, 10000},
				"axle_spacings_mm": []int{1801},
				"scale_weight_kg":  21001,
			})
		if err != nil {
			return err
		}
		if code != http.StatusUnprocessableEntity {
			return fmt.Errorf("期望 422，实际 %d，响应 %s", code, body)
		}
		for _, kw := range []string{"groups", "vehicle", "violations", "calibration"} {
			if bytes.Contains(body, []byte(kw)) {
				return fmt.Errorf("422 响应夹带了部分结果（%s）: %s", kw, body)
			}
		}
		var errResp struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(body, &errResp); err != nil || errResp.Error == "" {
			return fmt.Errorf("422 响应缺少 error 字段: %s", body)
		}
		return nil
	})

	// 携带地磅重量的同一请求两次，响应同样必须逐字节一致（校准结果可重复）。
	check("相同地磅校准请求两次响应逐字节一致", func() error {
		req := map[string]any{
			"axle_loads_kg":    []int{9200, 9200},
			"axle_spacings_mm": []int{1800},
			"scale_weight_kg":  18000,
		}
		_, first, err := postJSON(ctx, client, base+"/api/v1/verify", req)
		if err != nil {
			return err
		}
		_, second, err := postJSON(ctx, client, base+"/api/v1/verify", req)
		if err != nil {
			return err
		}
		if !bytes.Equal(first, second) {
			return fmt.Errorf("响应不一致:\n%s\n%s", first, second)
		}
		return nil
	})

	// 未携带地磅重量的 1800/1801 请求：响应与校准能力引入前逐字节一致。
	check("未携带地磅重量的 1800/1801 响应逐字节不变", func() error {
		cases := []struct {
			name string
			req  map[string]any
			want string
		}{
			{
				"1800mm",
				map[string]any{"axle_loads_kg": []int{9500, 9500}, "axle_spacings_mm": []int{1800}},
				`{"groups":[{"index":1,"start_axle":1,"end_axle":2,"axle_count":2,"load_kg":19000,"limit_kg":18000,"over_limit":true}],"vehicle":{"load_kg":19000,"limit_kg":49000,"over_limit":false},"violations":[{"scope":"group","index":1}]}`,
			},
			{
				"1801mm",
				map[string]any{"axle_loads_kg": []int{9500, 9500}, "axle_spacings_mm": []int{1801}},
				`{"groups":[{"index":1,"start_axle":1,"end_axle":1,"axle_count":1,"load_kg":9500,"limit_kg":10000,"over_limit":false},{"index":2,"start_axle":2,"end_axle":2,"axle_count":1,"load_kg":9500,"limit_kg":10000,"over_limit":false}],"vehicle":{"load_kg":19000,"limit_kg":49000,"over_limit":false},"violations":[]}`,
			},
		}
		for _, tc := range cases {
			code, body, err := postJSON(ctx, client, base+"/api/v1/verify", tc.req)
			if err != nil {
				return err
			}
			if code != http.StatusOK {
				return fmt.Errorf("%s 期望 200，实际 %d，响应 %s", tc.name, code, body)
			}
			if string(body) != tc.want {
				return fmt.Errorf("%s 响应发生变化:\n期望 %s\n实际 %s", tc.name, tc.want, body)
			}
		}
		return nil
	})

	if failures > 0 {
		fmt.Printf("\n验收未通过：%d 项失败\n", failures)
		os.Exit(1)
	}
	fmt.Println("\n全部验收项通过")
}

func waitReady(ctx context.Context, url string) error {
	client := &http.Client{Timeout: 2 * time.Second}
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if resp, err := client.Do(req); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func postJSON(ctx context.Context, client *http.Client, url string, payload any) (int, []byte, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return 0, nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(raw))
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return 0, nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return 0, nil, err
	}
	return resp.StatusCode, body, nil
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
