package verify

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSplitGroups_Boundary1800(t *testing.T) {
	// 1800 毫米：两轴同组。
	spans := splitGroups(2, []int{1800})
	require.Equal(t, []axleSpan{{start: 0, end: 1}}, spans)

	// 1801 毫米：开启下一组。
	spans = splitGroups(2, []int{1801})
	require.Equal(t, []axleSpan{{start: 0, end: 0}, {start: 1, end: 1}}, spans)

	// 500（下限）同组，10000（上限）另起一组。
	spans = splitGroups(3, []int{500, 10000})
	require.Equal(t, []axleSpan{{start: 0, end: 1}, {start: 2, end: 2}}, spans)
}

func TestEvaluate_BoundarySpacingFlipsConclusion(t *testing.T) {
	// 两侧载荷相同：1800mm 时双轴组 19000 > 18000 超限；
	// 1801mm 时拆成两个单轴组，各 9500 <= 10000，全部合规。
	res, err := Evaluate([]int{9500, 9500}, []int{1800})
	require.NoError(t, err)
	require.Len(t, res.Groups, 1)
	assert.Equal(t, GroupResult{
		Index: 1, StartAxle: 1, EndAxle: 2, AxleCount: 2,
		LoadKg: 19000, LimitKg: 18000, OverLimit: true,
	}, res.Groups[0])
	assert.Equal(t, []Violation{{Scope: "group", Index: 1}}, res.Violations)
	assert.False(t, res.Vehicle.OverLimit)

	res, err = Evaluate([]int{9500, 9500}, []int{1801})
	require.NoError(t, err)
	require.Len(t, res.Groups, 2)
	assert.Equal(t, 1, res.Groups[0].StartAxle)
	assert.Equal(t, 1, res.Groups[0].EndAxle)
	assert.Equal(t, 10000, res.Groups[0].LimitKg)
	assert.False(t, res.Groups[0].OverLimit)
	assert.Equal(t, 2, res.Groups[1].StartAxle)
	assert.False(t, res.Groups[1].OverLimit)
	assert.Empty(t, res.Violations)
}

func TestEvaluate_GroupLimitsEqualIsCompliant(t *testing.T) {
	cases := []struct {
		name      string
		loads     []int
		spacings  []int
		wantLimit int
		wantLoad  int
	}{
		{"单轴等于 10000 合规", []int{10000}, nil, 10000, 10000},
		{"双轴等于 18000 合规", []int{9000, 9000}, []int{1800}, 18000, 18000},
		{"三轴等于 24000 合规", []int{8000, 8000, 8000}, []int{1000, 1000}, 24000, 24000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Evaluate(tc.loads, tc.spacings)
			require.NoError(t, err)
			require.Len(t, res.Groups, 1)
			g := res.Groups[0]
			assert.Equal(t, tc.wantLoad, g.LoadKg)
			assert.Equal(t, tc.wantLimit, g.LimitKg)
			assert.False(t, g.OverLimit, "等于限值必须判定合规")
			assert.Empty(t, res.Violations)
		})
	}
}

func TestEvaluate_OneKiloOverLimitIsViolation(t *testing.T) {
	cases := []struct {
		name     string
		loads    []int
		spacings []int
	}{
		{"单轴 10001 超限", []int{10001}, nil},
		{"双轴 18001 超限", []int{10000, 8001}, []int{600}},
		{"三轴 24001 超限", []int{9000, 9000, 6001}, []int{700, 1800}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := Evaluate(tc.loads, tc.spacings)
			require.NoError(t, err)
			require.Len(t, res.Groups, 1)
			assert.True(t, res.Groups[0].OverLimit)
			assert.Equal(t, []Violation{{Scope: "group", Index: 1}}, res.Violations)
		})
	}
}

func TestEvaluate_FourOrMoreAxleGroupIllegal(t *testing.T) {
	// 连续 4 轴间距都 <=1800，形成四轴组，输入非法。
	res, err := Evaluate([]int{1, 1, 1, 1}, []int{1800, 1800, 1800})
	require.Error(t, err)
	assert.Nil(t, res)

	// 12 轴全部连续同组同样非法。
	loads := make([]int, 12)
	spacings := make([]int, 11)
	for i := range loads {
		loads[i] = 1
	}
	for i := range spacings {
		spacings[i] = 500
	}
	res, err = Evaluate(loads, spacings)
	require.Error(t, err)
	assert.Nil(t, res)

	// 三个三轴组合法（组间 1801 断开）。
	res, err = Evaluate(
		[]int{8000, 8000, 8000, 8000, 8000, 8000, 8000, 8000, 8000},
		[]int{1000, 1000, 1801, 1000, 1000, 1801, 1000, 1000},
	)
	require.NoError(t, err)
	require.Len(t, res.Groups, 3)
	for i, g := range res.Groups {
		assert.Equal(t, i+1, g.Index)
		assert.Equal(t, 3, g.AxleCount)
		assert.Equal(t, i*3+1, g.StartAxle)
		assert.Equal(t, i*3+3, g.EndAxle)
		assert.Equal(t, 24000, g.LimitKg)
	}
}

func TestEvaluate_VehicleLimitEqualIsCompliant(t *testing.T) {
	// 5 个单轴组（间距全 1801），合计 49000，整车等于限值合规。
	loads := []int{10000, 10000, 10000, 10000, 9000}
	res, err := Evaluate(loads, []int{1801, 1801, 1801, 1801})
	require.NoError(t, err)
	assert.Equal(t, 49000, res.Vehicle.LoadKg)
	assert.Equal(t, VehicleLimitKg, res.Vehicle.LimitKg)
	assert.False(t, res.Vehicle.OverLimit)
	assert.Empty(t, res.Violations)

	// 合计 49001：整车超限，固定置于超限清单最后。
	loads[4] = 9001
	res, err = Evaluate(loads, []int{1801, 1801, 1801, 1801})
	require.NoError(t, err)
	assert.True(t, res.Vehicle.OverLimit)
	assert.Equal(t, []Violation{{Scope: "vehicle"}}, res.Violations)
}

func TestEvaluate_ViolationsOrderedByGroupThenVehicle(t *testing.T) {
	// 4 个单轴组：第 2、4 组超限；整车合计 10001+10001+1+10001 = 30004，
	// 不超整车限值 -> 清单只含按组序号排列的两组。
	res, err := Evaluate(
		[]int{1, 10001, 1, 10001},
		[]int{1801, 1801, 1801},
	)
	require.NoError(t, err)
	assert.Equal(t, []Violation{
		{Scope: "group", Index: 2},
		{Scope: "group", Index: 4},
	}, res.Violations)

	// 所有组合规但整车超限：清单仅整车一项。
	res, err = Evaluate(
		[]int{10000, 10000, 10000, 10000, 10000},
		[]int{1801, 1801, 1801, 1801},
	)
	require.NoError(t, err)
	assert.True(t, res.Vehicle.OverLimit)
	assert.Equal(t, []Violation{{Scope: "vehicle"}}, res.Violations)

	// 组超限与整车超限并存：组按序号在前，整车固定最后。
	res, err = Evaluate(
		[]int{20000, 20000, 10000},
		[]int{1801, 1801},
	)
	require.NoError(t, err)
	assert.True(t, res.Groups[0].OverLimit)
	assert.True(t, res.Groups[1].OverLimit)
	assert.True(t, res.Vehicle.OverLimit)
	assert.Equal(t, 50000, res.Vehicle.LoadKg)
	assert.Equal(t, []Violation{
		{Scope: "group", Index: 1},
		{Scope: "group", Index: 2},
		{Scope: "vehicle"},
	}, res.Violations)
}

func TestEvaluate_AxleNumberingAndIntegerSums(t *testing.T) {
	// 间距 1800,1801,1800,1801,1800 -> 三个双轴组：(1,2) (3,4) (5,6)
	res, err := Evaluate(
		[]int{7000, 8000, 9000, 100, 200, 300},
		[]int{1800, 1801, 1800, 1801, 1800},
	)
	require.NoError(t, err)
	require.Len(t, res.Groups, 3)
	assert.Equal(t, [][2]int{{1, 2}, {3, 4}, {5, 6}},
		[][2]int{
			{res.Groups[0].StartAxle, res.Groups[0].EndAxle},
			{res.Groups[1].StartAxle, res.Groups[1].EndAxle},
			{res.Groups[2].StartAxle, res.Groups[2].EndAxle},
		})
	assert.Equal(t, 15000, res.Groups[0].LoadKg)
	assert.Equal(t, 9100, res.Groups[1].LoadKg)
	assert.Equal(t, 500, res.Groups[2].LoadKg)
	assert.Equal(t, 24600, res.Vehicle.LoadKg) // 整数求和
	assert.False(t, res.Vehicle.OverLimit)
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name     string
		loads    []int
		spacings []int
	}{
		{"轴数为 0", nil, nil},
		{"轴数为 13", make([]int, 13), make([]int, 12)},
		{"轴距少一项", []int{1, 2, 3}, []int{1000}},
		{"轴距多一项", []int{1, 2, 3}, []int{1000, 1000, 1000}},
		{"单轴却给了轴距", []int{5000}, []int{1000}},
		{"载荷为 0", []int{0}, nil},
		{"载荷为 20001", []int{20001}, nil},
		{"载荷为负数", []int{-1}, nil},
		{"轴距 499", []int{1, 1}, []int{499}},
		{"轴距 10001", []int{1, 1}, []int{10001}},
		{"轴距为 0", []int{1, 1}, []int{0}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.loads, tc.spacings)
			assert.Error(t, err)
			res, err2 := Evaluate(tc.loads, tc.spacings)
			assert.Error(t, err2)
			assert.Nil(t, res, "非法输入不得给出任何部分结果")
		})
	}
}

func TestValidate_BoundaryInputsAccepted(t *testing.T) {
	// 1 轴、12 轴；载荷 1/20000；轴距 500/10000 均为合法边界。
	res, err := Evaluate([]int{1}, nil)
	require.NoError(t, err)
	require.Len(t, res.Groups, 1)
	assert.Equal(t, 1, res.Groups[0].LoadKg)

	loads := make([]int, 12)
	spacings := make([]int, 11)
	for i := range loads {
		loads[i] = 1
	}
	loads[0], loads[11] = 20000, 20000
	for i := range spacings {
		spacings[i] = 10000 // 全部 >1800，12 个单轴组，避免四轴组非法
	}
	res, err = Evaluate(loads, spacings)
	require.NoError(t, err)
	require.Len(t, res.Groups, 12)
	assert.Equal(t, 40010, res.Vehicle.LoadKg)

	res, err = Evaluate([]int{1, 20000}, []int{500})
	require.NoError(t, err)
	require.Len(t, res.Groups, 1)
	assert.Equal(t, 20001, res.Groups[0].LoadKg)
	assert.True(t, res.Groups[0].OverLimit)
}

func TestCalibrate_PositiveDifferenceTieBreaksByAxleOrder(t *testing.T) {
	// 差额 +1：两轴小数部分相同（各 0.5），轴序号小的优先补 1。
	got := Calibrate([]int{9500, 9500}, 19001)
	require.Equal(t, []int{9501, 9500}, got)
}

func TestCalibrate_NegativeDifferenceTieBreaksByAxleOrder(t *testing.T) {
	// 差额 -1：各轴份额 -0.5 向下取整为 -1，小数部分相同，
	// 轴序号小的优先补回 1，差额最终落在尾轴。
	got := Calibrate([]int{9500, 9500}, 18999)
	require.Equal(t, []int{9500, 9499}, got)
}

func TestCalibrate_ProportionalToOriginalLoads(t *testing.T) {
	// 差额 +3 按 2:1:1 分摊：精确份额 1.5 / 0.75 / 0.75，取整后余 2 千克，
	// 小数部分 0.75 的第 2、3 轴（同余按轴序）各补 1。
	got := Calibrate([]int{10000, 5000, 5000}, 20003)
	require.Equal(t, []int{10001, 5001, 5001}, got)
}

func TestCalibrate_SumConservedAndRepeatable(t *testing.T) {
	cases := []struct {
		name  string
		loads []int
		scale int
	}{
		{"零差额原样返回", []int{1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1, 1}, 12},
		{"正差额", []int{3, 7, 11, 13, 17}, 55},
		{"负差额", []int{20000, 20000, 20000}, 58000},
		{"载荷大小悬殊", []int{1, 20000, 1, 20000, 1}, 38000},
		{"五轴负差额", []int{10000, 10000, 10000, 10000, 10000}, 49000},
		{"单轴直接取地磅值", []int{5000}, 5001},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			first := Calibrate(tc.loads, tc.scale)
			second := Calibrate(tc.loads, tc.scale)
			require.Equal(t, first, second, "同一输入必须得到同一校准结果")
			require.Len(t, first, len(tc.loads))
			sum := 0
			for _, v := range first {
				sum += v
			}
			require.Equal(t, tc.scale, sum, "校准后各轴之和必须严格等于地磅重量")
		})
	}
}

func TestEvaluateWithScale_CalibrationFlipsGroupVerdict(t *testing.T) {
	// 原始双轴组 18400 > 18000 超限；地磅 18000 校准为 [9000,9000]，
	// 组载荷 18000 等于限值合规，结论翻转。
	res, err := EvaluateWithScale([]int{9200, 9200}, []int{1800}, 18000)
	require.NoError(t, err)
	require.NotNil(t, res.Calibration)
	assert.Equal(t, CalibrationResult{
		ScaleWeightKg:     18000,
		OriginalTotalKg:   18400,
		DifferenceKg:      -400,
		CalibratedLoadsKg: []int{9000, 9000},
	}, *res.Calibration)
	require.Len(t, res.Groups, 1)
	assert.Equal(t, 18000, res.Groups[0].LoadKg)
	assert.False(t, res.Groups[0].OverLimit)
	assert.Equal(t, 18000, res.Vehicle.LoadKg)
	assert.False(t, res.Vehicle.OverLimit)
	assert.Empty(t, res.Violations)

	// 同一载荷不带地磅重量时仍超限，确认翻转来自校准而非规则变化。
	plain, err := Evaluate([]int{9200, 9200}, []int{1800})
	require.NoError(t, err)
	assert.True(t, plain.Groups[0].OverLimit)
	assert.Nil(t, plain.Calibration, "未携带地磅重量的结果不得含校准信息")
}

func TestEvaluateWithScale_CalibrationFlipsVehicleVerdict(t *testing.T) {
	// 5 个单轴组合计 49050 > 49000 整车超限；地磅 49000 校准后等于限值合规。
	loads := []int{9810, 9810, 9810, 9810, 9810}
	spacings := []int{1801, 1801, 1801, 1801}
	res, err := EvaluateWithScale(loads, spacings, 49000)
	require.NoError(t, err)
	require.NotNil(t, res.Calibration)
	assert.Equal(t, []int{9800, 9800, 9800, 9800, 9800}, res.Calibration.CalibratedLoadsKg)
	assert.Equal(t, 49000, res.Vehicle.LoadKg)
	assert.False(t, res.Vehicle.OverLimit)
	assert.Empty(t, res.Violations)
}

func TestEvaluateWithScale_DeviationBoundary(t *testing.T) {
	// 合计 20000：偏差恰好 5%（±1000）允许，1001 拒绝。
	res, err := EvaluateWithScale([]int{10000, 10000}, []int{1801}, 21000)
	require.NoError(t, err)
	assert.Equal(t, 21000, res.Vehicle.LoadKg)

	res, err = EvaluateWithScale([]int{10000, 10000}, []int{1801}, 19000)
	require.NoError(t, err)
	assert.Equal(t, 19000, res.Vehicle.LoadKg)

	for _, scale := range []int{21001, 18999} {
		res, err := EvaluateWithScale([]int{10000, 10000}, []int{1801}, scale)
		require.Error(t, err)
		assert.Nil(t, res, "偏差超界不得给出任何部分结果")
		assert.Contains(t, err.Error(), "偏差超过")
	}
}

func TestEvaluateWithScale_ScaleWeightRange(t *testing.T) {
	for _, scale := range []int{0, -1, 240001} {
		res, err := EvaluateWithScale([]int{10000, 10000}, []int{1801}, scale)
		require.Error(t, err)
		assert.Nil(t, res, "地磅重量越界不得给出任何部分结果")
	}

	// 边界 1 与 240000 合法（偏差须在 5% 以内）。
	res, err := EvaluateWithScale([]int{1}, nil, 1)
	require.NoError(t, err)
	assert.Equal(t, 1, res.Vehicle.LoadKg)

	loads := make([]int, 12)
	spacings := make([]int, 11)
	for i := range loads {
		loads[i] = 20000
	}
	for i := range spacings {
		spacings[i] = 10000 // 全部 >1800，12 个单轴组
	}
	res, err = EvaluateWithScale(loads, spacings, 240000)
	require.NoError(t, err)
	assert.Equal(t, 240000, res.Vehicle.LoadKg)
	assert.Equal(t, 0, res.Calibration.DifferenceKg)
	assert.Equal(t, loads, res.Calibration.CalibratedLoadsKg, "零差额时校准载荷与原载荷一致")
}

func TestEvaluateWithScale_InvalidAxleInputStillRejected(t *testing.T) {
	// 携带地磅重量不改变既有校验：四轴组、载荷越界等仍整体拒绝。
	res, err := EvaluateWithScale([]int{1, 1, 1, 1}, []int{1800, 1800, 1800}, 4)
	require.Error(t, err)
	assert.Nil(t, res)

	res, err = EvaluateWithScale([]int{0, 1}, []int{1000}, 1)
	require.Error(t, err)
	assert.Nil(t, res)
}
