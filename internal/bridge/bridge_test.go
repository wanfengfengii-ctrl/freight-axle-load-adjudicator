package bridge

import (
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 三轴车、桥面有效长度恰好等于首尾轴距：平移至车头轴抵达桥出口、
// 车尾轴抵达桥入口的瞬间（位移 3000），边界上的前后轴均计入载荷，
// 三轴合计 12000 达到峰值。
func TestAnalyze_BoundaryExactFitCountsEdgeAxles(t *testing.T) {
	in := Input{
		AxlePositionsMm: []int{0, 1500, 3000},
		AxleLoadsKg:     []int{4000, 4000, 4000},
		BridgeLengthMm:  3000,
		ApprovedLoadKg:  12000,
	}
	got, err := Analyze(in)
	require.NoError(t, err)
	assert.Equal(t, &Analysis{
		MaxLoadKg:      12000,
		FirstAxle:      1,
		LastAxle:       3,
		DisplacementMm: 3000,
		Conclusion:     ConclusionPass, // 等于核定载荷判定为通行
	}, got)
}

// 同一车辆、核定载荷低于峰值 1 千克：平移后出现的峰值触发拦停。
func TestAnalyze_PeakAfterTranslationStops(t *testing.T) {
	in := Input{
		AxlePositionsMm: []int{0, 1500, 3000},
		AxleLoadsKg:     []int{4000, 4000, 4000},
		BridgeLengthMm:  3000,
		ApprovedLoadKg:  11999,
	}
	got, err := Analyze(in)
	require.NoError(t, err)
	assert.Equal(t, 12000, got.MaxLoadKg)
	assert.Equal(t, 3000, got.DisplacementMm, "峰值出现在平移之后")
	assert.Equal(t, ConclusionStop, got.Conclusion)
}

// 并列峰值：{2,3} 轴在位移 2000、{1,2} 轴在位移 4000 都达到 8000，
// 按车辆位移最小确定唯一结果（首轴序号更大的候选反而落选，可锁定裁决次序）。
func TestAnalyze_TiedPeaksChooseEarliestDisplacement(t *testing.T) {
	in := Input{
		AxlePositionsMm: []int{0, 2000, 4000},
		AxleLoadsKg:     []int{5000, 3000, 5000},
		BridgeLengthMm:  2000,
		ApprovedLoadKg:  8000,
	}
	got, err := Analyze(in)
	require.NoError(t, err)
	assert.Equal(t, &Analysis{
		MaxLoadKg:      8000,
		FirstAxle:      2,
		LastAxle:       3,
		DisplacementMm: 2000,
		Conclusion:     ConclusionPass,
	}, got)
}

// 相邻轴距大于桥长：两轴不会同时落桥，峰值为较重的单轴。
func TestAnalyze_GapLargerThanBridgeNeverShares(t *testing.T) {
	in := Input{
		AxlePositionsMm: []int{0, 6000},
		AxleLoadsKg:     []int{3000, 5000},
		BridgeLengthMm:  3000,
		ApprovedLoadKg:  10000,
	}
	got, err := Analyze(in)
	require.NoError(t, err)
	assert.Equal(t, &Analysis{
		MaxLoadKg:      5000,
		FirstAxle:      2,
		LastAxle:       2,
		DisplacementMm: 0,
		Conclusion:     ConclusionPass,
	}, got)
}

// 单轴车：进入即峰值，位移为 0。
func TestAnalyze_SingleAxle(t *testing.T) {
	got, err := Analyze(Input{
		AxlePositionsMm: []int{1200},
		AxleLoadsKg:     []int{9000},
		BridgeLengthMm:  1000,
		ApprovedLoadKg:  9000,
	})
	require.NoError(t, err)
	assert.Equal(t, &Analysis{
		MaxLoadKg:      9000,
		FirstAxle:      1,
		LastAxle:       1,
		DisplacementMm: 0,
		Conclusion:     ConclusionPass,
	}, got)

	// 同一单轴车，核定载荷差 1 千克即拦停。
	got, err = Analyze(Input{
		AxlePositionsMm: []int{1200},
		AxleLoadsKg:     []int{9000},
		BridgeLengthMm:  1000,
		ApprovedLoadKg:  8999,
	})
	require.NoError(t, err)
	assert.Equal(t, ConclusionStop, got.Conclusion)
}

// 整车长度小于桥长：全部轴同时落桥的时段内载荷为车货总重，
// 最早取得该最大值的位移是车尾轴抵达桥入口的时刻。
func TestAnalyze_WholeVehicleOnBridge(t *testing.T) {
	got, err := Analyze(Input{
		AxlePositionsMm: []int{0, 1000, 2000},
		AxleLoadsKg:     []int{1000, 2000, 3000},
		BridgeLengthMm:  10000,
		ApprovedLoadKg:  6000,
	})
	require.NoError(t, err)
	assert.Equal(t, &Analysis{
		MaxLoadKg:      6000,
		FirstAxle:      1,
		LastAxle:       3,
		DisplacementMm: 2000,
		Conclusion:     ConclusionPass,
	}, got)
}

// 锁定边界恰好容纳场景的事件扫描序列：同位移处进入先于离开，
// 位移 3000 处先形成三轴全落桥区间，再收缩为两轴区间。
func TestScanWindows_EnterBeforeLeaveAtSameDisplacement(t *testing.T) {
	windows := ScanWindows(Input{
		AxlePositionsMm: []int{0, 1500, 3000},
		AxleLoadsKg:     []int{4000, 4000, 4000},
		BridgeLengthMm:  3000,
		ApprovedLoadKg:  12000,
	})
	require.Equal(t, []Window{
		{FirstAxle: 3, LastAxle: 3, DisplacementMm: 0, LoadKg: 4000},
		{FirstAxle: 2, LastAxle: 3, DisplacementMm: 1500, LoadKg: 8000},
		{FirstAxle: 1, LastAxle: 3, DisplacementMm: 3000, LoadKg: 12000},
		{FirstAxle: 1, LastAxle: 2, DisplacementMm: 3000, LoadKg: 8000},
		{FirstAxle: 1, LastAxle: 1, DisplacementMm: 4500, LoadKg: 4000},
	}, windows)
}

// 轴距大于桥长时落桥区间会出现空档：空档不产出区间，事件扫描仍完整。
func TestScanWindows_EmptyGapProducesNoWindow(t *testing.T) {
	windows := ScanWindows(Input{
		AxlePositionsMm: []int{0, 6000},
		AxleLoadsKg:     []int{3000, 5000},
		BridgeLengthMm:  3000,
		ApprovedLoadKg:  10000,
	})
	require.Equal(t, []Window{
		{FirstAxle: 2, LastAxle: 2, DisplacementMm: 0, LoadKg: 5000},
		{FirstAxle: 1, LastAxle: 1, DisplacementMm: 6000, LoadKg: 3000},
	}, windows)
}

// 相同输入两次分析，结果完全一致（唯一确定结论）。
func TestAnalyze_Deterministic(t *testing.T) {
	in := Input{
		AxlePositionsMm: []int{0, 2000, 4000},
		AxleLoadsKg:     []int{5000, 3000, 5000},
		BridgeLengthMm:  2000,
		ApprovedLoadKg:  8000,
	}
	first, err := Analyze(in)
	require.NoError(t, err)
	second, err := Analyze(in)
	require.NoError(t, err)
	assert.Equal(t, first, second)
}

func TestValidate_Errors(t *testing.T) {
	valid := func() Input {
		return Input{
			AxlePositionsMm: []int{0, 1500, 3000},
			AxleLoadsKg:     []int{4000, 4000, 4000},
			BridgeLengthMm:  3000,
			ApprovedLoadKg:  12000,
		}
	}
	cases := []struct {
		name      string
		mutate    func(*Input)
		wantInErr string
	}{
		{"轴数为零", func(in *Input) { in.AxlePositionsMm, in.AxleLoadsKg = nil, nil }, "轴数"},
		{"轴数十三", func(in *Input) {
			in.AxlePositionsMm = []int{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12}
			in.AxleLoadsKg = []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}
		}, "轴数"},
		{"位置项数与轴数不一致", func(in *Input) { in.AxlePositionsMm = []int{0, 1500} }, "一致"},
		{"载荷为零", func(in *Input) { in.AxleLoadsKg[1] = 0 }, "载荷"},
		{"载荷为负", func(in *Input) { in.AxleLoadsKg[0] = -100 }, "载荷"},
		{"载荷超上限", func(in *Input) { in.AxleLoadsKg[2] = 20001 }, "载荷"},
		{"载荷极大", func(in *Input) { in.AxleLoadsKg[0] = math.MaxInt64 }, "载荷"},
		{"位置为负", func(in *Input) { in.AxlePositionsMm[0] = -1 }, "超出允许范围"},
		{"位置超上限", func(in *Input) { in.AxlePositionsMm[2] = 100001 }, "超出允许范围"},
		{"位置极大", func(in *Input) { in.AxlePositionsMm[2] = math.MaxInt64 }, "超出允许范围"},
		{"位置重复", func(in *Input) { in.AxlePositionsMm = []int{0, 1500, 1500} }, "重复"},
		{"位置未递增", func(in *Input) { in.AxlePositionsMm = []int{0, 3000, 1500} }, "严格递增"},
		{"桥长低于下限", func(in *Input) { in.BridgeLengthMm = 999 }, "桥面有效长度"},
		{"桥长高于上限", func(in *Input) { in.BridgeLengthMm = 50001 }, "桥面有效长度"},
		{"核定载荷为零", func(in *Input) { in.ApprovedLoadKg = 0 }, "核定载荷"},
		{"核定载荷高于上限", func(in *Input) { in.ApprovedLoadKg = 200001 }, "核定载荷"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := valid()
			tc.mutate(&in)
			err := Validate(in)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.wantInErr)

			// 非法输入不得产出任何分析结果。
			res, err := Analyze(in)
			require.Error(t, err)
			assert.Nil(t, res)
		})
	}
}

// 边界值合法：桥长 1000/50000、核定载荷 1/200000、载荷 20000、位置 0/100000
// 均须通过校验。
func TestValidate_BoundaryValuesAccepted(t *testing.T) {
	for _, in := range []Input{
		{AxlePositionsMm: []int{0}, AxleLoadsKg: []int{1}, BridgeLengthMm: 1000, ApprovedLoadKg: 1},
		{AxlePositionsMm: []int{0}, AxleLoadsKg: []int{1}, BridgeLengthMm: 50000, ApprovedLoadKg: 200000},
		{AxlePositionsMm: []int{0, 49000}, AxleLoadsKg: []int{1, 1}, BridgeLengthMm: 1000, ApprovedLoadKg: 1},
		{AxlePositionsMm: []int{0, 100000}, AxleLoadsKg: []int{20000, 20000}, BridgeLengthMm: 50000, ApprovedLoadKg: 40000},
	} {
		assert.NoError(t, Validate(in), "%+v", in)
	}
}

// 溢出回归：极大轴位置曾使“进入位移+桥长”回绕成负位移、极大载荷曾使求和
// 回绕，导致最大桥面载荷被报成零或少算。现在这类输入一律被校验拒绝，
// 绝不产出错误分析结果。
func TestAnalyze_ExtremeValuesRejectedNoOverflow(t *testing.T) {
	cases := []struct {
		name string
		in   Input
	}{
		{"极大轴位置", Input{
			AxlePositionsMm: []int{0, math.MaxInt64},
			AxleLoadsKg:     []int{4000, 4000},
			BridgeLengthMm:  3000,
			ApprovedLoadKg:  12000,
		}},
		{"极大轴载荷", Input{
			AxlePositionsMm: []int{0, 1500},
			AxleLoadsKg:     []int{math.MaxInt64, math.MaxInt64},
			BridgeLengthMm:  3000,
			ApprovedLoadKg:  200000,
		}},
		{"极大位置与载荷", Input{
			AxlePositionsMm: []int{math.MaxInt64 - 1, math.MaxInt64},
			AxleLoadsKg:     []int{math.MaxInt64, 1},
			BridgeLengthMm:  50000,
			ApprovedLoadKg:  200000,
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Analyze(tc.in)
			require.Error(t, err)
			assert.Nil(t, res)
		})
	}
}

// 上限边界处的最重车辆：12 轴各 20000 千克、位置达 100000 毫米，
// 全部整数运算不溢出，落桥载荷合计 240000 千克被正确算出。
func TestAnalyze_MaximumVehicleComputesExactly(t *testing.T) {
	positions := make([]int, 0, MaxAxles)
	loads := make([]int, 0, MaxAxles)
	for i := 0; i < MaxAxles; i++ {
		positions = append(positions, i*9000) // 末轴位置 99000，贴上限
		loads = append(loads, MaxAxleLoadKg)
	}
	got, err := Analyze(Input{
		AxlePositionsMm: positions,
		AxleLoadsKg:     loads,
		BridgeLengthMm:  MaxBridgeLengthMm,
		ApprovedLoadKg:  MaxApprovedLoadKg,
	})
	require.NoError(t, err)
	// 桥长 50000 可容纳连续 6 轴（跨度 45000），峰值 6×20000=120000。
	assert.Equal(t, 120000, got.MaxLoadKg)
	assert.Equal(t, ConclusionPass, got.Conclusion)
}
