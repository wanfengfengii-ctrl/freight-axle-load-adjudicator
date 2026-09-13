// Package httpapi 提供车轴复核的 HTTP 接口与请求契约。
package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"

	"axleverify/internal/verify"
)

// maxBodyBytes 限制请求体大小，防止超大数组绕过 1-12 轴的约束消耗资源。
const maxBodyBytes = 1 << 20

type verifyRequest struct {
	AxleLoadsKg    []int `json:"axle_loads_kg"`
	AxleSpacingsMm []int `json:"axle_spacings_mm"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// successResponse 成功裁决响应：先列各组，再列整车，最后列超限清单。
type successResponse struct {
	Groups     []verify.GroupResult `json:"groups"`
	Vehicle    verify.VehicleResult `json:"vehicle"`
	Violations []verify.Violation   `json:"violations"`
}

// Register 在给定引擎上注册全部路由。
func Register(r *gin.Engine) {
	r.GET("/healthz", healthz)
	r.POST("/api/v1/verify", handleVerify)
}

func healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func handleVerify(c *gin.Context) {
	// 输入非法时统一走 422，且在产出任何裁决结果之前拒绝，杜绝部分结果。
	var req verifyRequest
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&req); err != nil {
		respond422(c, describeDecodeError(err))
		return
	}
	// 顶层必须恰好只有一个 JSON 值，再解码一次时合法请求只能得到 io.EOF。
	// 不能用 dec.More()：它只反映数组/对象上下文内是否还有元素，
	// 对顶层孤立的右括号（如 "...}]"）会漏判。
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			respond422(c, "请求体中存在多个 JSON 值，只允许一个 JSON 对象")
			return
		}
		respond422(c, describeDecodeError(err))
		return
	}
	if req.AxleLoadsKg == nil {
		respond422(c, "缺少必填字段 axle_loads_kg（按车头到车尾排列的轴载荷，千克）")
		return
	}
	if req.AxleSpacingsMm == nil {
		respond422(c, "缺少必填字段 axle_spacings_mm（相邻轴距，毫米，项数须比轴数少一项）")
		return
	}

	result, err := verify.Evaluate(req.AxleLoadsKg, req.AxleSpacingsMm)
	if err != nil {
		respond422(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, successResponse{
		Groups:     result.Groups,
		Vehicle:    result.Vehicle,
		Violations: result.Violations,
	})
}

func respond422(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(http.StatusUnprocessableEntity, errorResponse{Error: msg})
}

func describeDecodeError(err error) string {
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "请求体为空或 JSON 不完整，须提交包含 axle_loads_kg 与 axle_spacings_mm 的 JSON 对象"
	case errors.As(err, new(*json.UnmarshalTypeError)):
		return "字段类型错误：axle_loads_kg 与 axle_spacings_mm 必须为整数数组"
	default:
		// 含语法错误、未知字段、数字写入整型失败（如 1.5、超大数）等。
		return "JSON 解析失败：" + err.Error()
	}
}
