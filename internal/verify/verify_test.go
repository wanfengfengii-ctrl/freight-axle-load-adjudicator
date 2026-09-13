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
