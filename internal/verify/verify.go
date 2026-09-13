// Package verify 实现货车轴组划分与超限裁决规则，不包含任何 HTTP 细节。
package verify

import "fmt"

// 输入与裁决常量，单位见字段名。
const (
	MinAxles       = 1
	MaxAxles       = 12
	MinAxleLoadKg  = 1
	MaxAxleLoadKg  = 20000
	MinSpacingMm   = 500
	MaxSpacingMm   = 10000
	SameGroupMaxMm = 1800 // 相邻轴距 <= 1800mm 同组，> 1800mm 另起一组
	VehicleLimitKg = 49000
)

// groupLimitKg 按轴组内轴数给出限值：单轴 10000、双轴 18000、三轴 24000。
var groupLimitKg = map[int]int{
	1: 10000,
	2: 18000,
	3: 24000,
}

// GroupResult 为单个轴组的裁决结果，轴序号均按车头到车尾从 1 开始编号。
type GroupResult struct {
	Index     int  `json:"index"`      // 组序号，从 1 开始
	StartAxle int  `json:"start_axle"` // 组内首轴序号
	EndAxle   int  `json:"end_axle"`   // 组内尾轴序号
	AxleCount int  `json:"axle_count"` // 组内轴数（1-3）
	LoadKg    int  `json:"load_kg"`    // 组载荷，整数求和
	LimitKg   int  `json:"limit_kg"`   // 适用限值
	OverLimit bool `json:"over_limit"` // 是否超限；等于限值判定为合规
}

// VehicleResult 为整车合计裁决结果。
type VehicleResult struct {
	LoadKg    int  `json:"load_kg"`
	LimitKg   int  `json:"limit_kg"`
	OverLimit bool `json:"over_limit"`
}

// Violation 标记一处超限：轴组超限按组序号升序排列，整车超限固定在最后。
type Violation struct {
	Scope string `json:"scope"`           // "group" 或 "vehicle"
	Index int    `json:"index,omitempty"` // scope 为 group 时的组序号
}

// Result 是一次复核的完整裁决结果，不含任何错误信息（非法输入不会产生部分结果）。
type Result struct {
	Groups     []GroupResult `json:"groups"`
	Vehicle    VehicleResult `json:"vehicle"`
	Violations []Violation   `json:"violations"`
}

// Validate 仅校验输入合法性，不产出裁决结果。
func Validate(axleLoadsKg []int, axleSpacingsMm []int) error {
	n := len(axleLoadsKg)
	if n < MinAxles || n > MaxAxles {
		return fmt.Errorf("轴数须为 %d 至 %d，实际为 %d", MinAxles, MaxAxles, n)
	}
	if len(axleSpacingsMm) != n-1 {
		return fmt.Errorf("轴距项数必须比轴数少一项：轴数 %d 时应为 %d，实际为 %d",
			n, n-1, len(axleSpacingsMm))
	}
	for i, load := range axleLoadsKg {
		if load < MinAxleLoadKg || load > MaxAxleLoadKg {
			return fmt.Errorf("第 %d 轴载荷 %d 超出允许范围 %d-%d 千克",
				i+1, load, MinAxleLoadKg, MaxAxleLoadKg)
		}
	}
	for i, spacing := range axleSpacingsMm {
		if spacing < MinSpacingMm || spacing > MaxSpacingMm {
			return fmt.Errorf("第 %d 项轴距 %d 超出允许范围 %d-%d 毫米",
				i+1, spacing, MinSpacingMm, MaxSpacingMm)
		}
	}
	return nil
}

// axleSpan 描述一个轴组覆盖的轴下标区间（闭区间，0 基）。
type axleSpan struct {
	start int
	end   int
}

// splitGroups 按 1800mm 临界规则划分轴组：间距 <= 1800 同组，> 1800 开启下一组。
func splitGroups(axleCount int, spacingsMm []int) []axleSpan {
	spans := make([]axleSpan, 0, axleCount)
	start := 0
	for i := 0; i < axleCount-1; i++ {
		if spacingsMm[i] > SameGroupMaxMm {
			spans = append(spans, axleSpan{start: start, end: i})
			start = i + 1
		}
	}
	spans = append(spans, axleSpan{start: start, end: axleCount - 1})
	return spans
}

// Evaluate 校验并裁决。任何输入非法都返回 error，调用方必须整体拒绝，不得使用部分结果。
func Evaluate(axleLoadsKg []int, axleSpacingsMm []int) (*Result, error) {
	if err := Validate(axleLoadsKg, axleSpacingsMm); err != nil {
		return nil, err
	}

	spans := splitGroups(len(axleLoadsKg), axleSpacingsMm)
	groups := make([]GroupResult, 0, len(spans))
	violations := make([]Violation, 0)
	vehicleLoad := 0

	for gi, span := range spans {
		count := span.end - span.start + 1
		if count >= 4 {
			return nil, fmt.Errorf("第 %d 轴组包含 %d 轴（首尾轴为第 %d、%d 轴），形成四轴及以上轴组时输入非法",
				gi+1, count, span.start+1, span.end+1)
		}
		load := 0
		for ax := span.start; ax <= span.end; ax++ {
			load += axleLoadsKg[ax]
		}
		limit := groupLimitKg[count]
		over := load > limit // 等于限值合规
		groups = append(groups, GroupResult{
			Index:     gi + 1,
			StartAxle: span.start + 1,
			EndAxle:   span.end + 1,
			AxleCount: count,
			LoadKg:    load,
			LimitKg:   limit,
			OverLimit: over,
		})
		if over {
			violations = append(violations, Violation{Scope: "group", Index: gi + 1})
		}
		vehicleLoad += load
	}

	vehicle := VehicleResult{
		LoadKg:    vehicleLoad,
		LimitKg:   VehicleLimitKg,
		OverLimit: vehicleLoad > VehicleLimitKg,
	}
	if vehicle.OverLimit {
		violations = append(violations, Violation{Scope: "vehicle"})
	}

	return &Result{Groups: groups, Vehicle: vehicle, Violations: violations}, nil
}
