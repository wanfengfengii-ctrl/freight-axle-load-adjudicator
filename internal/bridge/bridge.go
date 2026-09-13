// Package bridge 实现临时便桥桥面承载窗口分析：车辆沿车头方向平移通过桥面时，
// 扫描各轴进入与离开有效桥面的事件，求任一时刻落在有效桥面的轴载合计最大值，
// 并给出通行或拦停结论。本包为独立领域逻辑，不调用轴组裁决与重测比对。
//
// 轴位置与轴载荷只设下限（位置非负、载荷为正），不设上限：落桥载荷按任意精度
// 整数求和，离开事件位移的排序对超出 int 范围的情形做了保序处理，极大数值
// 不会溢出，最大桥面载荷不会被报成零或少算。
package bridge

import (
	"fmt"
	"math"
	"math/big"
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
	// MinSpeedMmPerS / MaxSpeedMmPerS 为选填预计车速（车辆匀速通过桥面）的
	// 允许范围：提供时额外生成超载区段与累计时长，缺省或 nil 时不生成。
	MinSpeedMmPerS = 1
	MaxSpeedMmPerS = 50000
)

// 分析结论：最大桥面载荷不超过核定载荷（含相等）时通行，否则拦停。
const (
	ConclusionPass = "通行"
	ConclusionStop = "拦停"
)

// Input 为一次桥面承载窗口分析的输入：轴位置按车头方向严格递增（车尾轴在前、
// 车头轴在后），载荷与轴一一对应，桥长为桥面有效长度，核定载荷为现场核定值。
//
// SpeedMmPerS 选填：车辆匀速通过桥面的预计车速（毫米每秒）。缺省（nil）时
// 只执行既有峰值分析，结果与引入车速能力前逐字段一致；提供时额外生成超载
// 区段与累计时长。车速约束为 MinSpeedMmPerS～MaxSpeedMmPerS（均含）。
type Input struct {
	AxlePositionsMm []int
	AxleLoadsKg     []int
	BridgeLengthMm  int
	ApprovedLoadKg  int
	SpeedMmPerS     *int
}

// Window 为领域对象“连续落桥轴区间”：车辆平移过程中，落在有效桥面的轴始终
// 构成一段连续区间，本结构记录该区间形成时的首尾轴序号（1 基，按提交顺序）、
// 当时的车辆位移与落桥轴载合计（任意精度整数，极大载荷不溢出）。
type Window struct {
	FirstAxle      int
	LastAxle       int
	DisplacementMm int
	LoadKg         *big.Int
}

// Analysis 为桥面承载窗口分析结果：整个平移过程中桥面载荷的最大值、
// 取得最大值时的首尾轴序号与车辆位移，以及通行或拦停结论。
//
// 提供预计车速时（Input.SpeedMmPerS 非 nil）额外给出超载区段 OverloadSegments
// 与累计超载毫秒 TotalOverloadDurationMs；缺省时二者均为 nil，
// 调用方据此保持与既有成功响应逐字节一致。
type Analysis struct {
	MaxLoadKg      *big.Int // 最大桥面载荷，任意精度整数，千克
	FirstAxle      int      // 取得最大值时的首轴序号（1 基）
	LastAxle       int      // 取得最大值时的尾轴序号（1 基）
	DisplacementMm int      // 取得最大值时的车辆位移，毫米
	Conclusion     string   // ConclusionPass 或 ConclusionStop

	// OverloadSegments 为位移长度大于零且恒定载荷超过核定载荷的区段，按起始
	// 位移升序；相邻且载荷相同的区段先合并；同位移边界产生的瞬时峰值
	// （长度为零）不进入区段。仅在提供车速时生成。
	OverloadSegments []OverloadSegment
	// TotalOverloadDurationMs 为全部超载区段持续毫秒数之和；每段毫秒数按
	// “位移差 × 1000 ÷ 车速”向上取整。任意精度整数，低速大位移不溢出；
	// 仅在提供车速时生成（缺省为 nil）。
	TotalOverloadDurationMs *big.Int
}

// OverloadSegment 为一段载荷恒定且超过核定载荷的位移区间 [StartMm, EndMm)：
// 起点为某一进入/离开事件的位移，终点为下一个不同位移的事件，区间内无事件、
// 桥面载荷恒为 LoadKg；持续毫秒数 DurationMs = ceil((EndMm-StartMm)×1000/车速)。
// 位移、载荷与毫秒数均以任意精度整数给出，极大车辆跨度与低速情形不溢出。
type OverloadSegment struct {
	StartMm    *big.Int // 起始位移（含），毫米
	EndMm      *big.Int // 终止位移（不含），毫米
	LoadKg     *big.Int // 该段恒定桥面载荷，千克
	DurationMs *big.Int // 按预计车速匀速通过该段的持续时间，向上取整毫秒
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
	if in.SpeedMmPerS != nil {
		speed := *in.SpeedMmPerS
		if speed < MinSpeedMmPerS || speed > MaxSpeedMmPerS {
			return fmt.Errorf("预计车速 %d 超出允许范围 %d-%d 毫米每秒",
				speed, MinSpeedMmPerS, MaxSpeedMmPerS)
		}
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
	if best.LoadKg.Cmp(big.NewInt(int64(in.ApprovedLoadKg))) > 0 {
		conclusion = ConclusionStop
	}
	result := &Analysis{
		MaxLoadKg:      best.LoadKg,
		FirstAxle:      best.FirstAxle,
		LastAxle:       best.LastAxle,
		DisplacementMm: best.DisplacementMm,
		Conclusion:     conclusion,
	}
	// 仅在提供预计车速时追加超载区段与累计时长；缺省（nil）时结果与
	// 引入车速能力前完全一致。
	if in.SpeedMmPerS != nil {
		result.OverloadSegments, result.TotalOverloadDurationMs =
			OverloadSegments(in)
	}
	return result, nil
}

// OverloadSegments 复用轴进入、离开事件生成相邻事件位移之间的恒定载荷区段，
// 只保留位移长度大于零且载荷严格超过核定载荷的区段；相邻且载荷相同的区段先
// 合并。每段持续毫秒数按“位移差 × 1000 ÷ 车速”向上取整（任意精度整数运算，
// 极大位移与毫秒数不溢出），返回全部区段及毫秒数之和。
//
// 同一位移处进入先于离开：该瞬间“进入后、离开前”的峰值状态不占据任何正长度
// 位移区间，因此边界同位移产生的瞬时峰值照常出现在 ScanWindows 的快照中参与
// 原峰值裁决，却不会进入区段或累计时长——瞬时超载只影响原峰值结论。
// 调用前必须完成 Validate（含车速 1～50000 的校验）。
func OverloadSegments(in Input) ([]OverloadSegment, *big.Int) {
	events := buildEvents(in)
	approved := big.NewInt(int64(in.ApprovedLoadKg))
	speed := big.NewInt(int64(*in.SpeedMmPerS))

	segments := make([]OverloadSegment, 0)
	total := new(big.Int)
	// open 为尚未结算的超载区段；prevD 为上一组事件的位移，prevLoad 为该组
	// 事件全部处理后（同位移处进入先于离开）的桥面载荷，也即下一区间的恒定载荷。
	var open *OverloadSegment
	var prevD *big.Int
	prevLoad := new(big.Int)
	prevEmpty := true
	flush := func(seg *OverloadSegment) {
		length := new(big.Int).Sub(seg.EndMm, seg.StartMm)
		// ceil(位移差 × 1000 ÷ 车速) = (位移差 × 1000 + 车速 - 1) ÷ 车速。
		duration := new(big.Int).Mul(length, big.NewInt(1000))
		duration.Add(duration, speed)
		duration.Sub(duration, big.NewInt(1))
		duration.Quo(duration, speed)
		seg.DurationMs = duration
		total.Add(total, duration)
		segments = append(segments, *seg)
	}
	// closeOpen 在区间载荷不能并入当前区段（不超载，或载荷变化）时结算它。
	closeOpen := func() {
		if open != nil {
			flush(open)
			open = nil
		}
	}
	for _, group := range eventGroups(events, in.BridgeLengthMm) {
		if prevD != nil && group.d.Cmp(prevD) > 0 && !prevEmpty {
			// 区间 [prevD, group.d) 内无事件，载荷恒为 prevLoad。
			if prevLoad.Cmp(approved) > 0 {
				if open != nil && open.EndMm.Cmp(prevD) == 0 && open.LoadKg.Cmp(prevLoad) == 0 {
					open.EndMm = new(big.Int).Set(group.d) // 相邻且载荷相同：合并
				} else {
					closeOpen()
					open = &OverloadSegment{
						StartMm: new(big.Int).Set(prevD),
						EndMm:   new(big.Int).Set(group.d),
						LoadKg:  new(big.Int).Set(prevLoad),
					}
				}
			} else {
				closeOpen()
			}
		}
		prevD = new(big.Int).Set(group.d)
		prevLoad = new(big.Int).Set(group.load)
		prevEmpty = group.empty
	}
	closeOpen()
	return segments, total
}

// eventGroup 为同一位移处全部事件处理完毕后的状态：精确位移（可能超出 int
// 范围）、桥面载荷以及区间是否为空。
type eventGroup struct {
	d     *big.Int
	load  *big.Int
	empty bool
}

// eventGroups 按排好序的事件流把同一位移处的事件归为一组（进入先于离开），
// 逐组累计载荷并给出每组之后的桥面状态；离开位移超出 int 范围时按
// “进入位移 + 桥长”以任意精度整数精确给出。
func eventGroups(events []axleEvent, bridgeLengthMm int) []eventGroup {
	groups := make([]eventGroup, 0, len(events))
	load := new(big.Int)
	onBridge := 0
	for i := 0; i < len(events); {
		d := eventDisplacement(events[i], bridgeLengthMm)
		j := i + 1
		for j < len(events) {
			dj := eventDisplacement(events[j], bridgeLengthMm)
			if dj.Cmp(d) != 0 {
				break
			}
			j++
		}
		for _, ev := range events[i:j] {
			weight := big.NewInt(int64(ev.axleLoadKg))
			if ev.enter {
				load.Add(load, weight)
				onBridge++
			} else {
				load.Sub(load, weight)
				onBridge--
			}
		}
		groups = append(groups, eventGroup{
			d:     d,
			load:  new(big.Int).Set(load),
			empty: onBridge == 0,
		})
		i = j
	}
	return groups
}

// eventDisplacement 给出事件的精确位移：溢出 int 的离开事件按
// “进入位移 + 桥长”以任意精度整数给出，极大车辆跨度不丢精度。
func eventDisplacement(ev axleEvent, bridgeLengthMm int) *big.Int {
	d := big.NewInt(int64(ev.displacementMm))
	if ev.beyondInt {
		d.Add(d, big.NewInt(int64(bridgeLengthMm)))
	}
	return d
}

// axleEvent 为一根轴进入或离开有效桥面的事件。
type axleEvent struct {
	displacementMm int  // 事件发生时的车辆位移，毫米（可表示时）
	axle           int  // 轴下标（0 基，按提交顺序）
	axleLoadKg     int  // 该轴载荷，千克，分组累计时使用
	enter          bool // true 进入、false 离开
	// beyondInt 仅用于离开事件：离开位移 = 进入位移 + 桥长 超出 int 范围时
	// 为 true，此时 displacementMm 存进入位移。数学上该离开位移大于任何
	// 可表示位移，排序时置于全部可表示事件之后；溢出的离开事件之间按
	// 进入位移排序（同加桥长不改变相对次序），全程无需计算溢出值。
	beyondInt bool
}

// buildEvents 生成并排序车辆平移过程中的全部进入、离开事件：同一位移处进入
// 事件先于离开事件处理（边界闭区间上的两根轴一并计入）；离开位移超出 int
// 范围的事件保序置于最后，不计算溢出值。调用前必须完成 Validate。
func buildEvents(in Input) []axleEvent {
	n := len(in.AxlePositionsMm)
	head := in.AxlePositionsMm[n-1] // 车头轴位置（最大）
	events := make([]axleEvent, 0, 2*n)
	for i, pos := range in.AxlePositionsMm {
		enter := head - pos // 轴 i 抵达桥入口时的车辆位移；head >= pos，不会下溢
		events = append(events, axleEvent{
			displacementMm: enter,
			axle:           i,
			axleLoadKg:     in.AxleLoadsKg[i],
			enter:          true,
		})
		leave := axleEvent{axle: i, axleLoadKg: in.AxleLoadsKg[i]}
		if enter <= math.MaxInt-in.BridgeLengthMm {
			leave.displacementMm = enter + in.BridgeLengthMm
		} else {
			leave.displacementMm = enter
			leave.beyondInt = true
		}
		events = append(events, leave)
	}
	// 同一位移处进入事件先于离开事件处理：前轴恰抵桥出口、后轴恰抵桥入口的
	// 瞬间，两根边界轴都落在闭区间桥面上，须在同一时刻一并计入载荷。
	sort.Slice(events, func(a, b int) bool {
		x, y := events[a], events[b]
		if x.beyondInt != y.beyondInt {
			return !x.beyondInt // 可表示事件在前，溢出离开事件排在最后
		}
		if x.displacementMm != y.displacementMm {
			return x.displacementMm < y.displacementMm
		}
		return x.enter && !y.enter
	})
	return events
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
	events := buildEvents(in)

	// 进入与离开事件均按轴序号从大到小到达：进入的新轴接在区间首端，
	// 离开的轴从区间尾端收缩，落桥轴因此始终保持连续区间。
	// 载荷按任意精度整数累计，极大载荷求和不会溢出。
	windows := make([]Window, 0, len(events))
	first, last := n, n-1 // first > last 表示空区间
	load := new(big.Int)
	for _, ev := range events {
		if ev.enter {
			first = ev.axle
			load.Add(load, big.NewInt(int64(ev.axleLoadKg)))
		} else {
			last = ev.axle - 1
			load.Sub(load, big.NewInt(int64(ev.axleLoadKg)))
		}
		if first > last {
			continue // 空区间不产出
		}
		if ev.beyondInt {
			// 位移超出 int 范围的离开事件不产出快照：所有进入事件都排在它
			// 之前，此后只剩离开事件，区间载荷严格单调下降，被跳过区间的
			// 载荷必小于收缩前已记录的状态，不可能成为峰值；其位移本身也
			// 无法用 int 表示。
			continue
		}
		windows = append(windows, Window{
			FirstAxle:      first + 1,
			LastAxle:       last + 1,
			DisplacementMm: ev.displacementMm,
			LoadKg:         new(big.Int).Set(load),
		})
	}
	return windows
}

// preferred 判定候选区间 a 是否优于 b：载荷更大者优先；载荷相同取车辆位移
// 更小者；位移也相同取首轴序号更小者。该次序保证相同最大值出现多次时
// 仍能确定唯一结果。
func preferred(a, b Window) bool {
	if c := a.LoadKg.Cmp(b.LoadKg); c != 0 {
		return c > 0
	}
	if a.DisplacementMm != b.DisplacementMm {
		return a.DisplacementMm < b.DisplacementMm
	}
	return a.FirstAxle < b.FirstAxle
}
