package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func doWheelBalance(t *testing.T, r http.Handler, body string) (int, []byte) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wheel-balance", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	return w.Code, w.Body.Bytes()
}

// 完全平衡：偏差为 0、放行。
func TestWheelBalance_PerfectlyBalanced(t *testing.T) {
	r := newRouter(t)
	code, body := doWheelBalance(t, r,
		`{"left_wheel_loads_kg":[5000,1000],"right_wheel_loads_kg":[5000,1000],"tolerance_permille":50}`)
	require.Equal(t, http.StatusOK, code, string(body))
	require.JSONEq(t, `{
		"axles":[
			{"total_kg":10000,"imbalance_permille":0,"over_tolerance":false},
			{"total_kg":2000,"imbalance_permille":0,"over_tolerance":false}
		],
		"conclusion":"放行"
	}`, string(body))
}

// 恰好等于阈值：over_tolerance=false、放行；再大 1 千克即翻转。
func TestWheelBalance_ExactlyAtThreshold(t *testing.T) {
	r := newRouter(t)
	// 总重 2000、差 100：100000 == 2000×50，恰好等于阈值。
	code, body := doWheelBalance(t, r,
		`{"left_wheel_loads_kg":[1050],"right_wheel_loads_kg":[950],"tolerance_permille":50}`)
	require.Equal(t, http.StatusOK, code, string(body))
	assert.JSONEq(t, `{"axles":[{"total_kg":2000,"imbalance_permille":50,"over_tolerance":false}],"conclusion":"放行"}`,
		string(body))

	// 差 102：102000 > 100000，超界、要求复检。
	code, body = doWheelBalance(t, r,
		`{"left_wheel_loads_kg":[1051],"right_wheel_loads_kg":[949],"tolerance_permille":50}`)
	require.Equal(t, http.StatusOK, code, string(body))
	var got struct {
		Axles []struct {
			TotalKg           int  `json:"total_kg"`
			ImbalancePermille int  `json:"imbalance_permille"`
			OverTolerance     bool `json:"over_tolerance"`
		} `json:"axles"`
		Conclusion string `json:"conclusion"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got.Axles, 1)
	assert.Equal(t, 2000, got.Axles[0].TotalKg)
	assert.Equal(t, 51, got.Axles[0].ImbalancePermille)
	assert.True(t, got.Axles[0].OverTolerance)
	assert.Equal(t, "要求复检", got.Conclusion)
}

// 超过阈值时按轴序返回全部超界轴，未超界轴夹杂其间。
func TestWheelBalance_OverThresholdAxlesInOrder(t *testing.T) {
	r := newRouter(t)
	code, body := doWheelBalance(t, r, `{
		"left_wheel_loads_kg":[1040,1050,1060,948,1000],
		"right_wheel_loads_kg":[960,950,940,1052,1000],
		"tolerance_permille":50
	}`)
	require.Equal(t, http.StatusOK, code, string(body))
	var got struct {
		Axles []struct {
			TotalKg           int  `json:"total_kg"`
			ImbalancePermille int  `json:"imbalance_permille"`
			OverTolerance     bool `json:"over_tolerance"`
		} `json:"axles"`
		Conclusion string `json:"conclusion"`
	}
	require.NoError(t, json.Unmarshal(body, &got))
	require.Len(t, got.Axles, 5)
	wantOver := []bool{false, false, true, true, false}
	wantPermille := []int{40, 50, 60, 52, 0}
	for i := range wantOver {
		assert.Equal(t, 2000, got.Axles[i].TotalKg, "第 %d 轴总重", i+1)
		assert.Equal(t, wantPermille[i], got.Axles[i].ImbalancePermille, "第 %d 轴偏差千分比", i+1)
		assert.Equal(t, wantOver[i], got.Axles[i].OverTolerance, "第 %d 轴超界标志", i+1)
	}
	assert.Equal(t, "要求复检", got.Conclusion)
}

// 单轴总重超过 300000：422 且错误信封之外不夹带任何评估结果。
func TestWheelBalance_AxleTotalOver300000Rejected(t *testing.T) {
	r := newRouter(t)
	code, body := doWheelBalance(t, r,
		`{"left_wheel_loads_kg":[200000],"right_wheel_loads_kg":[100001],"tolerance_permille":50}`)
	require.Equal(t, http.StatusUnprocessableEntity, code, string(body))
	assertWheelBalanceEnvelopeOnly(t, body)
}

// 两侧数组长度不一致：只出现错误信封。
func TestWheelBalance_LengthMismatchEnvelopeOnly(t *testing.T) {
	r := newRouter(t)
	cases := map[string]string{
		"右轮少一项":   `{"left_wheel_loads_kg":[1000,1000],"right_wheel_loads_kg":[1000],"tolerance_permille":50}`,
		"左轮少一项":   `{"left_wheel_loads_kg":[1000],"right_wheel_loads_kg":[1000,1000],"tolerance_permille":50}`,
		"两侧均为空数组": `{"left_wheel_loads_kg":[],"right_wheel_loads_kg":[],"tolerance_permille":50}`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			code, body := doWheelBalance(t, r, raw)
			require.Equal(t, http.StatusUnprocessableEntity, code, string(body))
			assertWheelBalanceEnvelopeOnly(t, body)
		})
	}
}

// 字段缺失、null、类型错误、越界、未知字段（含大小写变体）、非 JSON 媒体类型：
// 一律 422 且不夹带任何评估结果。
func TestWheelBalance_InvalidRequestsEnvelopeOnly(t *testing.T) {
	r := newRouter(t)
	cases := map[string]string{
		"缺左轮字段":      `{"right_wheel_loads_kg":[1000],"tolerance_permille":50}`,
		"缺右轮字段":      `{"left_wheel_loads_kg":[1000],"tolerance_permille":50}`,
		"缺阈值字段":      `{"left_wheel_loads_kg":[1000],"right_wheel_loads_kg":[1000]}`,
		"左轮为 null":   `{"left_wheel_loads_kg":null,"right_wheel_loads_kg":[1000],"tolerance_permille":50}`,
		"阈值为 null":   `{"left_wheel_loads_kg":[1000],"right_wheel_loads_kg":[1000],"tolerance_permille":null}`,
		"数组元素为 null": `{"left_wheel_loads_kg":[null],"right_wheel_loads_kg":[1000],"tolerance_permille":50}`,
		"数组元素为字符串":   `{"left_wheel_loads_kg":["1000"],"right_wheel_loads_kg":[1000],"tolerance_permille":50}`,
		"数组元素为小数":    `{"left_wheel_loads_kg":[1000.5],"right_wheel_loads_kg":[1000],"tolerance_permille":50}`,
		"阈值为字符串":     `{"left_wheel_loads_kg":[1000],"right_wheel_loads_kg":[1000],"tolerance_permille":"50"}`,
		"载荷为零":       `{"left_wheel_loads_kg":[0],"right_wheel_loads_kg":[1000],"tolerance_permille":50}`,
		"载荷超 200000": `{"left_wheel_loads_kg":[200001],"right_wheel_loads_kg":[1],"tolerance_permille":50}`,
		"阈值为负":       `{"left_wheel_loads_kg":[1000],"right_wheel_loads_kg":[1000],"tolerance_permille":-1}`,
		"阈值超 1000":   `{"left_wheel_loads_kg":[1000],"right_wheel_loads_kg":[1000],"tolerance_permille":1001}`,
		"未知字段":       `{"left_wheel_loads_kg":[1000],"right_wheel_loads_kg":[1000],"tolerance_permille":50,"extra":1}`,
		"大写字段变体":     `{"LEFT_WHEEL_LOADS_KG":[1000],"right_wheel_loads_kg":[1000],"tolerance_permille":50}`,
		"JSON 语法损坏":  `{"left_wheel_loads_kg":[1000],`,
		"请求体不是对象":    `[1000,1000,50]`,
		"多 JSON 值":   `{"left_wheel_loads_kg":[1000],"right_wheel_loads_kg":[1000],"tolerance_permille":50}1`,
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			code, body := doWheelBalance(t, r, raw)
			require.Equal(t, http.StatusUnprocessableEntity, code, "%s: %s", name, body)
			assertWheelBalanceEnvelopeOnly(t, body)
		})
	}
}

// 非 JSON 媒体类型：即使载荷合法也按 JSON 请求契约 422 拒绝。
func TestWheelBalance_NonJSONContentTypeRejected(t *testing.T) {
	r := newRouter(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/wheel-balance",
		bytes.NewBufferString(`{"left_wheel_loads_kg":[1000],"right_wheel_loads_kg":[1000],"tolerance_permille":50}`))
	req.Header.Set("Content-Type", "text/plain")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
	assertWheelBalanceEnvelopeOnly(t, w.Body.Bytes())

	// 缺失 Content-Type 同样 422。
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/wheel-balance",
		bytes.NewBufferString(`{"left_wheel_loads_kg":[1000],"right_wheel_loads_kg":[1000],"tolerance_permille":50}`))
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusUnprocessableEntity, w.Code, w.Body.String())
}

// 成功响应字段顺序固定为 axles 在前、conclusion 在后，且各轴字段顺序锁定。
func TestWheelBalance_ResponseFieldOrder(t *testing.T) {
	r := newRouter(t)
	code, body := doWheelBalance(t, r,
		`{"left_wheel_loads_kg":[1051],"right_wheel_loads_kg":[949],"tolerance_permille":50}`)
	require.Equal(t, http.StatusOK, code, string(body))
	assert.Equal(t,
		`{"axles":[{"total_kg":2000,"imbalance_permille":51,"over_tolerance":true}],"conclusion":"要求复检"}`,
		string(body))
}

// 既有入口与健康检查不受新路由影响。
func TestWheelBalance_ExistingRoutesUnaffected(t *testing.T) {
	r := newRouter(t)

	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, `{"status":"ok"}`, w.Body.String())

	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/api/v1/verify",
		bytes.NewBufferString(`{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1801]}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	assert.Contains(t, w.Body.String(), `"violations":[]`)
}

// assertWheelBalanceEnvelopeOnly 断言 422 响应只有 {"error":...} 信封，
// 不含任何评估产物字段。
func assertWheelBalanceEnvelopeOnly(t *testing.T, body []byte) {
	t.Helper()
	var envelope struct {
		Error string `json:"error"`
	}
	require.NoError(t, json.Unmarshal(body, &envelope), "响应应为 JSON: %s", body)
	assert.NotEmpty(t, envelope.Error)
	for _, kw := range []string{"axles", "conclusion", "total_kg", "imbalance_permille", "over_tolerance"} {
		assert.NotContains(t, string(body), kw, "422 响应夹带了评估结果（%s）: %s", kw, body)
	}
}
