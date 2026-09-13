// Package bridge 实现临时便桥桥面承载窗口分析：车辆沿车头方向平移通过桥面时，
// 扫描各轴进入与离开有效桥面的事件，求任一时刻落在有效桥面的轴载合计最大值，
// 并给出通行或拦停结论。本包为独立领域逻辑，不调用轴组裁决与重测比对。
package bridge

import (
	"fmt"
	"sort"
)

// 输入约束常量，单位见字段名。
const (
	MinAxles          = 1
	MaxAxles          = 12
	MinAxleLoadKg     = 1
	MinPositionMm     = 0
	MinBridgeLengthMm = 1000
	MaxBridgeLengthMm = 50000
	MinApprovedLoadKg = 1
	MaxApprovedLoadKg = 200000
)

// 分析结论：最大桥面载荷不超过核定载荷（含相等）时通行，否则拦停。
const (
	ConclusionPass = "通行"
	ConclusionStop = "拦停"
)

// Input 为一次桥面承载窗口分析的输入：轴位置按车头方向严格递增
// （第 1 轴为车尾，最后一轴为车头），载荷与轴一一对应，
// 桥长为桥面有效长度，核定载荷为现场核定值。
type Input struct {
	AxlePositionsMm []int
	AxleLoadsKg     []int
	BridgeLengthMm  int
	ApprovedLoadKg  int
}

// Window 为领域对象“连续落桥轴区间”：车辆平移过程中，落在有效桥面的轴
// 始终构成一段连续区间，本结构记录该区间在一次进入或离开事件发生后形成的
// 首尾轴序号（1 基，按提交顺序）、当时的车辆位移与落桥轴载合计。
type Window struct {
	FirstAxle      int // 区间首轴序号（1 基）
	LastAxle       int // 区间尾轴序号（1 基）
	DisplacementMm int // 区间形成时的车辆位移，毫米
	LoadKg         int // 落桥轴载合计，千克
}

// Analysis 为桥面承载窗口分析结果：整个平移过程中桥面载荷的最大值、
// 取得最大值时的首尾轴序号与车辆位移，以及通行或拦停结论。
type Analysis struct {
	MaxLoadKg      int    // 最大桥面载荷，千克
	FirstAxle      int    // 取得最大值时的首轴序号（1 基）
	LastAxle       int    // 取得最大值时的尾轴序号（1 基）
	DisplacementMm int    // 取得最大值时的车辆位移，毫米
	Conclusion     string // ConclusionPass 或 ConclusionStop
}

// Validate 仅校验输入合法性，不产出分析结果。
func Validate(in Input) error {
	n := len(in.AxleLoadsKg)
	if n < MinAxles || n > MaxAxles {
		return fmt.Errorf("轴数须为 %d 至 %d，实际为 %d", MinAxles, MaxAxles, n)
	}
	if len(in.AxlePositionsMm) != n {
		return fmt.Errorf("轴位置项数必须与轴数一致：轴数 %d 时应为 %d，实际为 %d",
			n, n, len(in.AxlePositionsMm))
	}
	for i, load := range in.AxleLoadsKg {
		if load < MinAxleLoadKg {
			return fmt.Errorf("第 %d 轴载荷 %d 必须不小于 %d 千克",
				i+1, load, MinAxleLoadKg)
		}
	}
	for i, pos := range in.AxlePositionsMm {
		if pos < MinPositionMm {
			return fmt.Errorf("第 %d 轴位置 %d 不得为负（毫米）", i+1, pos)
		}
		if i > 0 && pos <= in.AxlePositionsMm[i-1] {
			if pos == in.AxlePositionsMm[i-1] {
				return fmt.Errorf("第 %d 轴与第 %d 轴位置重复（均为 %d 毫米），轴位置不得重复",
					i, i+1, pos)
			}
			return fmt.Errorf("轴位置须按车头方向严格递增：第 %d 轴位置 %d 不大于第 %d 轴位置 %d（毫米）",
				i+1, pos, i, in.AxlePositionsMm[i-1])
		}
	}
	if in.BridgeLengthMm < MinBridgeLengthMm || in.BridgeLengthMm > MaxBridgeLengthMm {
		return fmt.Errorf("桥面有效长度 %d 超出允许范围 %d-%d 毫米",
			in.BridgeLengthMm, MinBridgeLengthMm, MaxBridgeLengthMm)
	}
	if in.ApprovedLoadKg < MinApprovedLoadKg || in.ApprovedLoadKg > MaxApprovedLoadKg {
		return fmt.Errorf("核定载荷 %d 超出允许范围 %d-%d 千克",
			in.ApprovedLoadKg, MinApprovedLoadKg, MaxApprovedLoadKg)
	}
	return nil
}

// Analyze 校验并执行桥面承载窗口分析。任何输入非法都返回 error，
// 调用方必须整体拒绝，不得使用部分结果。
func Analyze(in Input) (*Analysis, error) {
	if err := Validate(in); err != nil {
		return nil, err
	}
	windows := ScanWindows(in)
	best := windows[0]
	for _, w := range windows[1:] {
		if preferred(w, best) {
			best = w
		}
	}
	conclusion := ConclusionPass
	if best.LoadKg > in.ApprovedLoadKg {
		conclusion = ConclusionStop
	}
	return &Analysis{
		MaxLoadKg:      best.LoadKg,
		FirstAxle:      best.FirstAxle,
		LastAxle:       best.LastAxle,
		DisplacementMm: best.DisplacementMm,
		Conclusion:     conclusion,
	}, nil
}

// axleEvent 为一根轴进入或离开有效桥面的事件。
type axleEvent struct {
	displacementMm int  // 事件发生时的车辆位移，毫米
	axle           int  // 轴下标（0 基，按提交顺序）
	enter          bool // true 进入、false 离开
}

// ScanWindows 扫描车辆平移过程中的全部进入与离开事件，按位移升序给出每个
// 事件发生后形成的连续落桥轴区间（空区间不产出）。
//
// 位移原点为车头轴抵达桥入口（桥面坐标 0）的时刻，此后车辆每前进 1 毫米
// 位移加 1；轴 i 的桥面坐标 = 位移 − (车头轴位置 − 轴 i 位置)，坐标落在
// [0, 桥长] 闭区间内即视为落桥——桥面边界恰好容纳的轴（坐标 0 或桥长处）
// 同样计入载荷。调用前必须完成 Validate。
func ScanWindows(in Input) []Window {
	n := len(in.AxlePositionsMm)
	head := in.AxlePositionsMm[n-1] // 车头轴位置（最大）
	events := make([]axleEvent, 0, 2*n)
	for i, pos := range in.AxlePositionsMm {
		enter := head - pos // 轴 i 抵达桥入口时的车辆位移
		events = append(events,
			axleEvent{displacementMm: enter, axle: i, enter: true},
			axleEvent{displacementMm: enter + in.BridgeLengthMm, axle: i, enter: false},
		)
	}
	// 同一位移处进入事件先于离开事件处理：前轴恰抵桥出口、后轴恰抵桥入口的
	// 瞬间，两根边界轴都落在闭区间桥面上，须在同一时刻一并计入载荷。
	sort.Slice(events, func(a, b int) bool {
		if events[a].displacementMm != events[b].displacementMm {
			return events[a].displacementMm < events[b].displacementMm
		}
		return events[a].enter && !events[b].enter
	})

	// 进入与离开事件均按轴序号从大到小到达：进入的新轴接在区间首端，
	// 离开的轴从区间尾端收缩，落桥轴因此始终保持连续区间。
	windows := make([]Window, 0, len(events))
	first, last, load := n, n-1, 0 // first > last 表示空区间
	for _, ev := range events {
		if ev.enter {
			first = ev.axle
			load += in.AxleLoadsKg[ev.axle]
		} else {
			last = ev.axle - 1
			load -= in.AxleLoadsKg[ev.axle]
		}
		if first <= last {
			windows = append(windows, Window{
				FirstAxle:      first + 1,
				LastAxle:       last + 1,
				DisplacementMm: ev.displacementMm,
				LoadKg:         load,
			})
		}
	}
	return windows
}

// preferred 判定候选区间 a 是否优于 b：载荷更大者优先；载荷相同取车辆位移
// 更小者；位移也相同取首轴序号更小者。该次序保证相同最大值出现多次时
// 仍能确定唯一结果。
func preferred(a, b Window) bool {
	if a.LoadKg != b.LoadKg {
		return a.LoadKg > b.LoadKg
	}
	if a.DisplacementMm != b.DisplacementMm {
		return a.DisplacementMm < b.DisplacementMm
	}
	return a.FirstAxle < b.FirstAxle
}
