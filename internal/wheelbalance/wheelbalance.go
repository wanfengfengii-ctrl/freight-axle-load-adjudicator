// Package wheelbalance 实现便携轮重仪的左右轮重平衡评估：按轴交叉比较
// |左轮−右轮|×1000 与 该轴总轮重×允许偏差千分比，给出各轴偏差千分比、
// 是否超界与全车放行/复检结论。
//
// 本包为独立领域逻辑，不调用轴组裁决（internal/verify）、重测比对或
// 桥面承载窗口分析（internal/bridge）。全部比较与取整只用整数：是否超界
// 采用交叉乘法判定（|左−右|×1000 > 总轮重×阈值），偏差千分比按
// ceil(|左−右|×1000/总轮重) 向上取整，全程不出现浮点舍入，边界结论可重复。
package wheelbalance

import "fmt"

// 输入约束常量，单位见字段名。
const (
	MinAxles         = 1
	MaxAxles         = 12
	MinWheelLoadKg   = 1
	MaxWheelLoadKg   = 200000
	MaxAxleTotalKg   = 300000 // 单轴左右轮总重上限（含），超过即输入非法
	MinTolerancePerm = 0
	MaxTolerancePerm = 1000
	permilleScale    = 1000 // 千分比放大倍数
)

// 全车结论：任一轴超界即要求复检，全部不超界才放行。
const (
	ConclusionRelease   = "放行"
	ConclusionReinspect = "要求复检"
)

// AxleResult 为单个轴的左右轮平衡评估结果，轴序号按车头到车尾从 1 开始编号。
type AxleResult struct {
	TotalKg           int  `json:"total_kg"`           // 该轴左右轮总重，整数求和
	ImbalancePermille int  `json:"imbalance_permille"` // 偏差千分比 = ceil(|左−右|×1000/总重)，向上取整
	OverTolerance     bool `json:"over_tolerance"`     // 是否超出允许偏差；恰好等于阈值判定为不超界
}

// Result 为一次全车左右轮平衡评估的完整结果，不含任何错误信息
// （非法输入不会产生部分结果）。
type Result struct {
	Axles      []AxleResult `json:"axles"`
	Conclusion string       `json:"conclusion"` // ConclusionRelease 或 ConclusionReinspect
}

// Validate 仅校验输入合法性，不产出评估结果。
func Validate(leftKg, rightKg []int, tolerancePermille int) error {
	n := len(leftKg)
	if n < MinAxles || n > MaxAxles {
		return fmt.Errorf("轴数须为 %d 至 %d，实际为 %d", MinAxles, MaxAxles, n)
	}
	if len(rightKg) != n {
		return fmt.Errorf("左右轮载荷数组长度必须一致：左轮为 %d 项，右轮为 %d 项", n, len(rightKg))
	}
	if tolerancePermille < MinTolerancePerm || tolerancePermille > MaxTolerancePerm {
		return fmt.Errorf("允许偏差千分比 %d 超出允许范围 %d-%d",
			tolerancePermille, MinTolerancePerm, MaxTolerancePerm)
	}
	for i := 0; i < n; i++ {
		if v := leftKg[i]; v < MinWheelLoadKg || v > MaxWheelLoadKg {
			return fmt.Errorf("第 %d 轴左轮载荷 %d 超出允许范围 %d-%d 千克",
				i+1, v, MinWheelLoadKg, MaxWheelLoadKg)
		}
	}
	for i := 0; i < n; i++ {
		if v := rightKg[i]; v < MinWheelLoadKg || v > MaxWheelLoadKg {
			return fmt.Errorf("第 %d 轴右轮载荷 %d 超出允许范围 %d-%d 千克",
				i+1, v, MinWheelLoadKg, MaxWheelLoadKg)
		}
	}
	// 单项载荷合法后再校验单轴总重，保证报错针对的是总重而非某一侧。
	for i := 0; i < n; i++ {
		if total := leftKg[i] + rightKg[i]; total > MaxAxleTotalKg {
			return fmt.Errorf("第 %d 轴总轮重 %d 千克超过单轴上限 %d 千克",
				i+1, total, MaxAxleTotalKg)
		}
	}
	return nil
}

// Evaluate 校验并评估各轴左右轮平衡。任何输入非法都返回 error，
// 调用方必须整体拒绝，不得使用部分结果。
func Evaluate(leftKg, rightKg []int, tolerancePermille int) (*Result, error) {
	if err := Validate(leftKg, rightKg, tolerancePermille); err != nil {
		return nil, err
	}

	axles := make([]AxleResult, 0, len(leftKg))
	allWithin := true
	for i := range leftKg {
		total := leftKg[i] + rightKg[i]
		diff := leftKg[i] - rightKg[i]
		if diff < 0 {
			diff = -diff
		}
		// 交叉乘法判定：|左−右|×1000 > 总轮重×阈值 才算超界，
		// 恰好相等时放行；全程整数比较，不受浮点舍入影响。
		scaledDiff := diff * permilleScale
		over := scaledDiff > total*tolerancePermille
		if over {
			allWithin = false
		}
		// 偏差千分比向上取整：scaledDiff 与 total 均为正整数。
		permille := (scaledDiff + total - 1) / total
		axles = append(axles, AxleResult{
			TotalKg:           total,
			ImbalancePermille: permille,
			OverTolerance:     over,
		})
	}

	conclusion := ConclusionRelease
	if !allWithin {
		conclusion = ConclusionReinspect
	}
	return &Result{Axles: axles, Conclusion: conclusion}, nil
}
