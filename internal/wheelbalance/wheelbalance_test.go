package wheelbalance

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// 完全平衡：左右相等，偏差千分比为 0，全部放行。
func TestEvaluate_PerfectlyBalancedReleases(t *testing.T) {
	got, err := Evaluate([]int{5000, 1, 150000}, []int{5000, 1, 150000}, 50)
	require.NoError(t, err)
	require.Len(t, got.Axles, 3)
	assert.Equal(t, AxleResult{TotalKg: 10000, ImbalancePermille: 0, OverTolerance: false}, got.Axles[0])
	assert.Equal(t, AxleResult{TotalKg: 2, ImbalancePermille: 0, OverTolerance: false}, got.Axles[1])
	assert.Equal(t, AxleResult{TotalKg: 300000, ImbalancePermille: 0, OverTolerance: false}, got.Axles[2])
	assert.Equal(t, ConclusionRelease, got.Conclusion)
}

// 阈值为零时只有完全平衡才放行：任何左右差值都超界。
func TestEvaluate_ZeroTolerance(t *testing.T) {
	got, err := Evaluate([]int{5000}, []int{5000}, 0)
	require.NoError(t, err)
	assert.False(t, got.Axles[0].OverTolerance)
	assert.Equal(t, ConclusionRelease, got.Conclusion)

	got, err = Evaluate([]int{5000}, []int{4999}, 0)
	require.NoError(t, err)
	// |5000-4999|×1000 = 1000，总重 9999，ceil(1000/9999) = 1。
	assert.Equal(t, 1, got.Axles[0].ImbalancePermille)
	assert.True(t, got.Axles[0].OverTolerance)
	assert.Equal(t, ConclusionReinspect, got.Conclusion)
}

// 恰好等于阈值：|差|×1000 == 总重×阈值 时不超界、放行；
// 仅大 1（任何一侧差 1 千克）即翻转为超界。此用例若用浮点比值判定会产生
// 舍入分歧，交叉整数乘法保证边界结论唯一。
func TestEvaluate_ExactlyAtThresholdReleases(t *testing.T) {
	// 总重 2000、阈值 50‰：偏差恰好 50（差 100 千克）时 100×1000 == 2000×50。
	got, err := Evaluate([]int{1050}, []int{950}, 50)
	require.NoError(t, err)
	assert.Equal(t, 2000, got.Axles[0].TotalKg)
	assert.Equal(t, 50, got.Axles[0].ImbalancePermille)
	assert.False(t, got.Axles[0].OverTolerance, "恰好等于阈值应放行")
	assert.Equal(t, ConclusionRelease, got.Conclusion)

	// 总重为偶数时差也须为偶数：最小越界差值为 102 千克，102×1000=102000 > 100000，
	// 超界；ceil(102000/2000)=51。
	got, err = Evaluate([]int{1051}, []int{949}, 50)
	require.NoError(t, err)
	assert.Equal(t, 51, got.Axles[0].ImbalancePermille)
	assert.True(t, got.Axles[0].OverTolerance)
	assert.Equal(t, ConclusionReinspect, got.Conclusion)

	// 方向不影响结论：右轮重于左轮同样处理。
	got, err = Evaluate([]int{475}, []int{525}, 50)
	require.NoError(t, err)
	assert.False(t, got.Axles[0].OverTolerance)
	assert.Equal(t, 50, got.Axles[0].ImbalancePermille)
}

// 不能整除时偏差千分比向上取整，但超界仍按精确交叉乘法判定：
// 取整后的千分比可能恰好等于阈值，而实际比值仍小于阈值（不超界）。
func TestEvaluate_PermilleRoundedUpDoesNotFlipBoundary(t *testing.T) {
	// 总重 9999、差 1：精确比值约 0.10001‰，阈值 1‰ 时不超界；
	// ceil(1000/9999) = 1，上报千分比为 1 但 over_tolerance 必须为 false。
	got, err := Evaluate([]int{5000}, []int{4999}, 1)
	require.NoError(t, err)
	assert.Equal(t, 9999, got.Axles[0].TotalKg)
	assert.Equal(t, 1, got.Axles[0].ImbalancePermille)
	assert.False(t, got.Axles[0].OverTolerance, "向上取整为 1 不代表超过阈值 1")
	assert.Equal(t, ConclusionRelease, got.Conclusion)

	// 差 10：10×1000 = 10000 > 9999×1，恰好越过阈值，ceil(10000/9999) = 2。
	got, err = Evaluate([]int{5000}, []int{4989}, 1)
	require.NoError(t, err)
	assert.Equal(t, 2, got.Axles[0].ImbalancePermille)
	assert.True(t, got.Axles[0].OverTolerance)
}

// 超过阈值时按轴序返回全部超界轴，未超界轴夹杂其间也不影响清单顺序，
// 全车结论为要求复检。
func TestEvaluate_OverThresholdReturnsAllAxlesInOrder(t *testing.T) {
	// 阈值 50‰，各轴总重均为 1000：差 40/50/60/51/0 千克。
	got, err := Evaluate(
		[]int{520, 525, 530, 474, 500},
		[]int{480, 475, 470, 526, 500},
		50,
	)
	require.NoError(t, err)
	require.Len(t, got.Axles, 5)

	wantPermille := []int{40, 50, 60, 52, 0}
	wantOver := []bool{false, false, true, true, false}
	// 第 4 轴差 52：ceil(52000/1000)=52，且 52000 > 50000 超界。
	for i := range wantPermille {
		assert.Equal(t, 1000, got.Axles[i].TotalKg, "第 %d 轴总重", i+1)
		assert.Equal(t, wantPermille[i], got.Axles[i].ImbalancePermille, "第 %d 轴偏差千分比", i+1)
		assert.Equal(t, wantOver[i], got.Axles[i].OverTolerance, "第 %d 轴是否超界", i+1)
	}
	assert.Equal(t, ConclusionReinspect, got.Conclusion)
}

// 阈值 1000‰ 为千分比上限：合法单项均为正数时差值严格小于总重，
// 故该阈值下所有合法输入都不超界；再用 500‰ 验证边界翻转。
func TestEvaluate_Threshold1000Boundary(t *testing.T) {
	// 左轮 1、右轮 199999：总重 200000，差 199998，199998000 < 200000×1000，
	// 不超界；ceil(199998000/200000) = 1000。
	got, err := Evaluate([]int{1}, []int{199999}, 1000)
	require.NoError(t, err)
	assert.Equal(t, 200000, got.Axles[0].TotalKg)
	assert.Equal(t, 1000, got.Axles[0].ImbalancePermille)
	assert.False(t, got.Axles[0].OverTolerance)
	assert.Equal(t, ConclusionRelease, got.Conclusion)

	// 总重 200000、差 100000：100000×1000 == 200000×500，恰好等于 500‰，放行。
	got, err = Evaluate([]int{150000}, []int{50000}, 500)
	require.NoError(t, err)
	assert.False(t, got.Axles[0].OverTolerance)
	assert.Equal(t, 500, got.Axles[0].ImbalancePermille)

	// 差再大 1 千克即越过阈值，要求复检。
	got, err = Evaluate([]int{150001}, []int{49999}, 500)
	require.NoError(t, err)
	assert.True(t, got.Axles[0].OverTolerance)
	assert.Equal(t, ConclusionReinspect, got.Conclusion)
}

// 最大轴数 12 与单项边界 1、200000 合法。
func TestEvaluate_TwelveAxlesBoundaries(t *testing.T) {
	left := make([]int, 12)
	right := make([]int, 12)
	for i := range left {
		left[i] = 1
		right[i] = 200000
	}
	// 第 1 轴总重 200001 <= 300000 合法；全部轴差 199999，阈值 1000 时不超界。
	got, err := Evaluate(left, right, 1000)
	require.NoError(t, err)
	require.Len(t, got.Axles, 12)
	for _, a := range got.Axles {
		assert.Equal(t, 200001, a.TotalKg)
		assert.False(t, a.OverTolerance)
	}
	assert.Equal(t, ConclusionRelease, got.Conclusion)
}

func TestValidate_InvalidInputs(t *testing.T) {
	cases := []struct {
		name      string
		left      []int
		right     []int
		tolerance int
	}{
		{"空数组", nil, nil, 50},
		{"空数组字面量", []int{}, []int{}, 50},
		{"13 轴", make([]int, 13), make([]int, 13), 50},
		{"左右长度不一致", []int{1000, 1000}, []int{1000}, 50},
		{"左轮零值", []int{0}, []int{1000}, 50},
		{"右轮零值", []int{1000}, []int{0}, 50},
		{"左轮负值", []int{-1}, []int{1000}, 50},
		{"左轮超 200000", []int{200001}, []int{1}, 50},
		{"右轮超 200000", []int{1}, []int{200001}, 50},
		{"阈值为负", []int{1000}, []int{1000}, -1},
		{"阈值超 1000", []int{1000}, []int{1000}, 1001},
		{"单轴总重超 300000", []int{200000}, []int{100001}, 50},
		{"单轴总重明显超限", []int{200000}, []int{110000}, 50},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.left, tc.right, tc.tolerance)
			assert.Error(t, err)
			got, evalErr := Evaluate(tc.left, tc.right, tc.tolerance)
			assert.Nil(t, got, "非法输入不得返回部分结果")
			assert.Error(t, evalErr)
		})
	}
}

// 单轴总重恰好 300000（含边界）合法。
func TestValidate_AxleTotalExactly300000(t *testing.T) {
	got, err := Evaluate([]int{200000}, []int{100000}, 0)
	require.NoError(t, err)
	assert.Equal(t, 300000, got.Axles[0].TotalKg)
	assert.True(t, got.Axles[0].OverTolerance) // 阈值 0 且有差值
}
