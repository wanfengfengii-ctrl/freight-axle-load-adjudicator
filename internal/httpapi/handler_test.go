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
