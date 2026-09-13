package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"axleverify/internal/verify"
)

func newRouter(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	Register(r)
	return r
}

func doVerify(t *testing.T, r *gin.Engine, body string) (int, map[string]any) {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/verify", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)

	var out map[string]any
	if w.Body.Len() > 0 {
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &out), "响应应为 JSON: %s", w.Body.String())
	}
	return w.Code, out
}

func TestVerify_Boundary1800vs1801(t *testing.T) {
	r := newRouter(t)

	code, out := doVerify(t, r, `{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1800]}`)
	require.Equal(t, http.StatusOK, code)
	groups := out["groups"].([]any)
	require.Len(t, groups, 1)
	g0 := groups[0].(map[string]any)
	assert.Equal(t, float64(2), g0["axle_count"])
	assert.Equal(t, float64(19000), g0["load_kg"])
	assert.Equal(t, float64(18000), g0["limit_kg"])
	assert.Equal(t, true, g0["over_limit"])
	assert.Equal(t, float64(1), g0["start_axle"])
	assert.Equal(t, float64(2), g0["end_axle"])
	violations := out["violations"].([]any)
	require.Len(t, violations, 1)
	assert.Equal(t, "group", violations[0].(map[string]any)["scope"])

	code, out = doVerify(t, r, `{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1801]}`)
	require.Equal(t, http.StatusOK, code)
	require.Len(t, out["groups"].([]any), 2)
	assert.Empty(t, out["violations"])
	vehicle := out["vehicle"].(map[string]any)
	assert.Equal(t, float64(19000), vehicle["load_kg"])
	assert.Equal(t, float64(49000), vehicle["limit_kg"])
	assert.Equal(t, false, vehicle["over_limit"])
}

func TestVerify_GroupAndVehicleViolationsOrdered(t *testing.T) {
	r := newRouter(t)
	// 三个单轴组，前两组超限且整车 50000 超限；组按序号排列，整车最后。
	code, out := doVerify(t, r, `{"axle_loads_kg":[20000,20000,10000],"axle_spacings_mm":[1801,1801]}`)
	require.Equal(t, http.StatusOK, code)
	got := out["violations"].([]any)
	require.Len(t, got, 3)
	assert.Equal(t, map[string]any{"scope": "group", "index": float64(1)}, got[0])
	assert.Equal(t, map[string]any{"scope": "group", "index": float64(2)}, got[1])
	assert.Equal(t, map[string]any{"scope": "vehicle"}, got[2])
}

func TestVerify_422Cases(t *testing.T) {
	r := newRouter(t)
	cases := []struct {
		name string
		body string
	}{
		{"空体", ``},
		{"语法错误", `{"axle_loads_kg":[1,2],`},
		{"缺载荷字段", `{"axle_spacings_mm":[1000]}`},
		{"缺轴距字段", `{"axle_loads_kg":[1,2]}`},
		{"载荷非数组", `{"axle_loads_kg":100,"axle_spacings_mm":[]}`},
		{"元素非整数", `{"axle_loads_kg":[1.5,2],"axle_spacings_mm":[1000]}`},
		{"元素为字符串", `{"axle_loads_kg":["1",2],"axle_spacings_mm":[1000]}`},
		{"未知字段", `{"axle_loads_kg":[1,2],"axle_spacings_mm":[1000],"extra":1}`},
		{"轴数 0", `{"axle_loads_kg":[],"axle_spacings_mm":[]}`},
		{"轴数 13", `{"axle_loads_kg":[1,1,1,1,1,1,1,1,1,1,1,1,1],"axle_spacings_mm":[1000,1000,1000,1000,1000,1000,1000,1000,1000,1000,1000,1000]}`},
		{"轴距数量不符", `{"axle_loads_kg":[1,2,3],"axle_spacings_mm":[1000]}`},
		{"载荷超上限", `{"axle_loads_kg":[20001],"axle_spacings_mm":[]}`},
		{"轴距超上限", `{"axle_loads_kg":[1,1],"axle_spacings_mm":[10001]}`},
		{"轴距低于下限", `{"axle_loads_kg":[1,1],"axle_spacings_mm":[499]}`},
		{"四轴组非法", `{"axle_loads_kg":[1,1,1,1],"axle_spacings_mm":[1800,1800,1800]}`},
		{"两个 JSON 文档", `{"axle_loads_kg":[1],"axle_spacings_mm":[]}{}`},
		{"合法对象后多余右方括号", `{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1800]}]`},
		{"合法对象后多余右花括号", `{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1800]}}`},
		{"合法对象后多余数字", `{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1800]}1`},
		{"合法对象后多余字符串", `{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1800]}"x"`},
		{"合法对象后多余逗号", `{"axle_loads_kg":[1],"axle_spacings_mm":[]},`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			code, out := doVerify(t, r, tc.body)
			assert.Equal(t, http.StatusUnprocessableEntity, code)
			assert.Contains(t, out, "error")
			// 统一 422：绝不能夹带任何裁决结果。
			assert.NotContains(t, out, "groups")
			assert.NotContains(t, out, "vehicle")
			assert.NotContains(t, out, "violations")
		})
	}
}

func TestVerify_TrailingWhitespaceAccepted(t *testing.T) {
	r := newRouter(t)
	for _, body := range []string{
		`{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1801]}`,
		`{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1801]}` + "\n",
		`  {"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1801]}  ` + "\r\n\t",
	} {
		code, _ := doVerify(t, r, body)
		assert.Equal(t, http.StatusOK, code, "仅尾随空白必须接受: %q", body)
	}
}

func TestVerify_NullFieldsAre422(t *testing.T) {
	r := newRouter(t)
	for _, body := range []string{
		`{"axle_loads_kg":null,"axle_spacings_mm":[]}`,
		`{"axle_loads_kg":[1],"axle_spacings_mm":null}`,
	} {
		code, out := doVerify(t, r, body)
		assert.Equal(t, http.StatusUnprocessableEntity, code)
		assert.Contains(t, out["error"], "缺少必填字段")
	}
}

func TestHealthz(t *testing.T) {
	r := newRouter(t)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
	assert.JSONEq(t, `{"status":"ok"}`, w.Body.String())
}

// 与领域层常量保持一致，避免魔法数字漂移。
func TestLimitsMatchDomain(t *testing.T) {
	assert.Equal(t, 49000, verify.VehicleLimitKg)
}

func TestVerify_ScaleWeightCalibratesAndFlipsVerdict(t *testing.T) {
	r := newRouter(t)

	// 不带地磅重量：双轴组 18400 > 18000 超限，且无 calibration 字段。
	code, out := doVerify(t, r, `{"axle_loads_kg":[9200,9200],"axle_spacings_mm":[1800]}`)
	require.Equal(t, http.StatusOK, code)
	assert.Equal(t, true, out["groups"].([]any)[0].(map[string]any)["over_limit"])
	assert.NotContains(t, out, "calibration")

	// 携带地磅重量 18000：校准为 [9000,9000]，组载荷等于限值合规，结论翻转。
	code, out = doVerify(t, r, `{"axle_loads_kg":[9200,9200],"axle_spacings_mm":[1800],"scale_weight_kg":18000}`)
	require.Equal(t, http.StatusOK, code)
	cal, ok := out["calibration"].(map[string]any)
	require.True(t, ok, "携带地磅重量的响应必须含 calibration: %v", out)
	assert.Equal(t, float64(18000), cal["scale_weight_kg"])
	assert.Equal(t, float64(18400), cal["original_total_kg"])
	assert.Equal(t, float64(-400), cal["difference_kg"])
	assert.Equal(t, []any{float64(9000), float64(9000)}, cal["calibrated_loads_kg"])
	g0 := out["groups"].([]any)[0].(map[string]any)
	assert.Equal(t, float64(18000), g0["load_kg"])
	assert.Equal(t, false, g0["over_limit"])
	vehicle := out["vehicle"].(map[string]any)
	assert.Equal(t, float64(18000), vehicle["load_kg"])
	assert.Equal(t, false, vehicle["over_limit"])
	assert.Empty(t, out["violations"])
}

func TestVerify_ScaleWeightDeviationOver5PercentIs422(t *testing.T) {
	r := newRouter(t)
	// 合计 20000，地磅 21001 偏差 1001 > 5%：422 且只说明偏差超界，无任何部分结果。
	code, out := doVerify(t, r, `{"axle_loads_kg":[10000,10000],"axle_spacings_mm":[1801],"scale_weight_kg":21001}`)
	assert.Equal(t, http.StatusUnprocessableEntity, code)
	require.Contains(t, out, "error")
	assert.Contains(t, out["error"], "偏差超过")
	assert.NotContains(t, out, "groups")
	assert.NotContains(t, out, "vehicle")
	assert.NotContains(t, out, "violations")
	assert.NotContains(t, out, "calibration")

	// 恰好 5% 边界允许。
	code, out = doVerify(t, r, `{"axle_loads_kg":[10000,10000],"axle_spacings_mm":[1801],"scale_weight_kg":21000}`)
	assert.Equal(t, http.StatusOK, code)
	assert.Contains(t, out, "calibration")
}

func TestVerify_ScaleWeightInvalidIs422(t *testing.T) {
	r := newRouter(t)
	for _, body := range []string{
		`{"axle_loads_kg":[10000,10000],"axle_spacings_mm":[1801],"scale_weight_kg":0}`,
		`{"axle_loads_kg":[10000,10000],"axle_spacings_mm":[1801],"scale_weight_kg":-5}`,
		`{"axle_loads_kg":[10000,10000],"axle_spacings_mm":[1801],"scale_weight_kg":240001}`,
		`{"axle_loads_kg":[10000,10000],"axle_spacings_mm":[1801],"scale_weight_kg":1.5}`,
		`{"axle_loads_kg":[10000,10000],"axle_spacings_mm":[1801],"scale_weight_kg":"18000"}`,
	} {
		code, out := doVerify(t, r, body)
		assert.Equal(t, http.StatusUnprocessableEntity, code, body)
		assert.Contains(t, out, "error")
		assert.NotContains(t, out, "groups")
		assert.NotContains(t, out, "calibration")
	}
}

func TestVerify_NullScaleWeightTreatedAsAbsent(t *testing.T) {
	r := newRouter(t)
	code, out := doVerify(t, r, `{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1800],"scale_weight_kg":null}`)
	assert.Equal(t, http.StatusOK, code)
	assert.NotContains(t, out, "calibration")
}

// 未携带 scale_weight_kg 的响应必须与引入校准能力前逐字节一致。
func TestVerify_NoScaleWeightResponseByteIdentical(t *testing.T) {
	r := newRouter(t)
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			"1800mm 同组超限",
			`{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1800]}`,
			`{"groups":[{"index":1,"start_axle":1,"end_axle":2,"axle_count":2,"load_kg":19000,"limit_kg":18000,"over_limit":true}],"vehicle":{"load_kg":19000,"limit_kg":49000,"over_limit":false},"violations":[{"scope":"group","index":1}]}`,
		},
		{
			"1801mm 拆组合规",
			`{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1801]}`,
			`{"groups":[{"index":1,"start_axle":1,"end_axle":1,"axle_count":1,"load_kg":9500,"limit_kg":10000,"over_limit":false},{"index":2,"start_axle":2,"end_axle":2,"axle_count":1,"load_kg":9500,"limit_kg":10000,"over_limit":false}],"vehicle":{"load_kg":19000,"limit_kg":49000,"over_limit":false},"violations":[]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/api/v1/verify", bytes.NewBufferString(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(w, req)
			require.Equal(t, http.StatusOK, w.Code)
			assert.Equal(t, tc.want, w.Body.String())
		})
	}
}
