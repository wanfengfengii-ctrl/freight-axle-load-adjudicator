// Package httpapi 提供车轴复核的 HTTP 接口与请求契约。
package httpapi

import (
	"bytes"
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
	// ScaleWeightKg 选填：收费站地磅整车重量。提供时先按比例校准各轴载荷再裁决；
	// 缺省或 null 时不校准，响应与引入该校准能力前逐字节一致。
	ScaleWeightKg *int `json:"scale_weight_kg"`
}

// retestCompareRequest 为重测比对入口的请求：first 与 retest 各自携带一份
// 与单次裁决完全相同的测量数据（轴载荷、轴距及可选地磅重量）。
type retestCompareRequest struct {
	First  json.RawMessage `json:"first"`
	Retest json.RawMessage `json:"retest"`
}

type errorResponse struct {
	Error string `json:"error"`
}

// successResponse 成功裁决响应：先列各组，再列整车，最后列超限清单；
// 仅携带地磅重量的请求在末尾追加 calibration 校准信息。
type successResponse struct {
	Groups      []verify.GroupResult      `json:"groups"`
	Vehicle     verify.VehicleResult      `json:"vehicle"`
	Violations  []verify.Violation        `json:"violations"`
	Calibration *verify.CalibrationResult `json:"calibration,omitempty"`
}

// Register 在给定引擎上注册全部路由。
func Register(r *gin.Engine) {
	r.GET("/healthz", healthz)
	r.POST("/api/v1/verify", handleVerify)
	r.POST("/api/v1/retest-comparison", handleRetestComparison)
}

func healthz(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func handleVerify(c *gin.Context) {
	// 输入非法时统一走 422，且在产出任何裁决结果之前拒绝，杜绝部分结果。
	req, ok := decodeVerifyHandlerBody(c)
	if !ok {
		return
	}

	result, err := evaluateRequest(req)
	if err != nil {
		respond422(c, err.Error())
		return
	}

	c.JSON(http.StatusOK, successResponse{
		Groups:      result.Groups,
		Vehicle:     result.Vehicle,
		Violations:  result.Violations,
		Calibration: result.Calibration,
	})
}

func handleRetestComparison(c *gin.Context) {
	// 顶层先解析为结构体以拒绝未知字段并强制 first/retest 两个键；
	// 任一份数据非法或轴数不一致都整体 422，绝不夹带另一份裁决结果。
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes))
	dec.DisallowUnknownFields()
	var outer retestCompareRequest
	if err := dec.Decode(&outer); err != nil {
		respond422(c, describeCompareDecodeError(err))
		return
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			respond422(c, "请求体中存在多个 JSON 值，只允许一个 JSON 对象")
			return
		}
		respond422(c, describeCompareDecodeError(err))
		return
	}
	if outer.First == nil {
		respond422(c, "缺少必填字段 first（首次称重数据，字段契约与单次裁决相同）")
		return
	}
	if outer.Retest == nil {
		respond422(c, "缺少必填字段 retest（重测数据，字段契约与单次裁决相同）")
		return
	}

	firstReq, ok := decodeMeasurement(c, outer.First, "首次称重数据")
	if !ok {
		return
	}
	retestReq, ok := decodeMeasurement(c, outer.Retest, "重测数据")
	if !ok {
		return
	}

	comparison, err := verify.CompareRetest(
		verify.RetestMeasurement{
			AxleLoadsKg:    firstReq.AxleLoadsKg,
			AxleSpacingsMm: firstReq.AxleSpacingsMm,
			ScaleWeightKg:  firstReq.ScaleWeightKg,
		},
		verify.RetestMeasurement{
			AxleLoadsKg:    retestReq.AxleLoadsKg,
			AxleSpacingsMm: retestReq.AxleSpacingsMm,
			ScaleWeightKg:  retestReq.ScaleWeightKg,
		},
	)
	if err != nil {
		respond422(c, err.Error())
		return
	}
	c.JSON(http.StatusOK, comparison)
}

// decodeVerifyHandlerBody 读取并解析单次裁决请求：请求体大小受限、
// 未知字段拒绝、顶层必须恰好只有一个 JSON 值。
func decodeVerifyHandlerBody(c *gin.Context) (*verifyRequest, bool) {
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		respond422(c, describeDecodeError(err))
		return nil, false
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			respond422(c, "请求体中存在多个 JSON 值，只允许一个 JSON 对象")
			return nil, false
		}
		respond422(c, describeDecodeError(err))
		return nil, false
	}
	return decodeMeasurement(c, raw, "")
}

// decodeMeasurement 解析一份测量数据（轴载荷、轴距及可选地磅重量）。
// label 非空时（重测比对入口）所有错误描述都冠以“首次称重数据/重测数据”，
// 明确指出非法的是哪一份；label 为空时行为与单次裁决入口完全一致。
// data 来自 json.RawMessage，本身必定恰好是一个完整 JSON 值，无需再防多文档。
func decodeMeasurement(c *gin.Context, data []byte, label string) (*verifyRequest, bool) {
	// 比对入口中每一份测量数据都必须是 JSON 对象（拒绝数字、字符串、数组乃至 null）；
	// 单次裁决入口（label 为空）保持原有解码错误文案，故不做此前置检查。
	if label != "" {
		if trimmed := bytes.TrimLeft(data, " \t\r\n"); len(trimmed) == 0 || trimmed[0] != '{' {
			respond422(c, label+"必须为 JSON 对象（字段契约与单次裁决相同）")
			return nil, false
		}
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var req verifyRequest
	if err := dec.Decode(&req); err != nil {
		respond422(c, describeMeasurementDecodeError(label, err))
		return nil, false
	}
	if req.AxleLoadsKg == nil {
		respond422(c, labelMsg(label, "缺少必填字段 axle_loads_kg（按车头到车尾排列的轴载荷，千克）"))
		return nil, false
	}
	if req.AxleSpacingsMm == nil {
		respond422(c, labelMsg(label, "缺少必填字段 axle_spacings_mm（相邻轴距，毫米，项数须比轴数少一项）"))
		return nil, false
	}
	return &req, true
}

// labelMsg 为嵌套测量数据的错误描述加上“首次/重测”前缀；
// 单次裁决入口（label 为空）保持原有错误文案逐字节不变。
func labelMsg(label, msg string) string {
	if label == "" {
		return msg
	}
	return label + msg
}

// evaluateRequest 按单次裁决契约执行裁决：带地磅重量时先校准再裁决，否则直接裁决。
func evaluateRequest(req *verifyRequest) (*verify.Result, error) {
	if req.ScaleWeightKg != nil {
		return verify.EvaluateWithScale(req.AxleLoadsKg, req.AxleSpacingsMm, *req.ScaleWeightKg)
	}
	return verify.Evaluate(req.AxleLoadsKg, req.AxleSpacingsMm)
}

func respond422(c *gin.Context, msg string) {
	c.AbortWithStatusJSON(http.StatusUnprocessableEntity, errorResponse{Error: msg})
}

func describeDecodeError(err error) string {
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "请求体为空或 JSON 不完整，须提交包含 axle_loads_kg 与 axle_spacings_mm 的 JSON 对象"
	case errors.As(err, new(*json.UnmarshalTypeError)):
		return "字段类型错误：axle_loads_kg 与 axle_spacings_mm 必须为整数数组，scale_weight_kg 必须为整数"
	default:
		// 含语法错误、未知字段、数字写入整型失败（如 1.5、超大数）等。
		return "JSON 解析失败：" + err.Error()
	}
}

// describeCompareDecodeError 描述重测比对请求顶层（first/retest 外层）的解码错误。
func describeCompareDecodeError(err error) string {
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "请求体为空或 JSON 不完整，须提交包含 first 与 retest 两份测量数据的 JSON 对象"
	case errors.As(err, new(*json.UnmarshalTypeError)):
		return "字段类型错误：first 与 retest 必须各自为一个 JSON 对象"
	default:
		// 含语法错误、未知字段等。
		return "JSON 解析失败：" + err.Error()
	}
}

// describeMeasurementDecodeError 与 describeDecodeError 同义，但为重测比对中的
// 某一份数据冠以“首次称重数据/重测数据”，明确指出非法来源。
func describeMeasurementDecodeError(label string, err error) string {
	if label == "" {
		return describeDecodeError(err)
	}
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return label + "为空或 JSON 不完整，须提交包含 axle_loads_kg 与 axle_spacings_mm 的 JSON 对象"
	case errors.As(err, new(*json.UnmarshalTypeError)):
		return label + "的字段类型错误：axle_loads_kg 与 axle_spacings_mm 必须为整数数组，scale_weight_kg 必须为整数"
	default:
		return label + "的 JSON 解析失败：" + err.Error()
	}
}
