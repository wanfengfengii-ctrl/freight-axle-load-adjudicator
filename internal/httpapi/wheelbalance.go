package httpapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"axleverify/internal/wheelbalance"
)

// wheelBalanceRequest 为左右轮重平衡评估请求：两个数组按车头到车尾一一对应
// 各轴的左右轮载荷，tolerance_permille 为允许偏差千分比。三个字段均必填；
// 载荷解码为普通整数切片，数组中的 null 无法写入 int，会在解码处报类型错误。
type wheelBalanceRequest struct {
	LeftWheelLoadsKg  []int `json:"left_wheel_loads_kg"`
	RightWheelLoadsKg []int `json:"right_wheel_loads_kg"`
	TolerancePermille *int  `json:"tolerance_permille"`
}

// wheelBalanceAxleJSON 为单轴评估结果的对外契约结构，由独立领域结果逐项映射，
// HTTP 层不直接序列化领域结构。
type wheelBalanceAxleJSON struct {
	TotalKg           int  `json:"total_kg"`
	ImbalancePermille int  `json:"imbalance_permille"`
	OverTolerance     bool `json:"over_tolerance"`
}

// wheelBalanceResponse 为全车左右轮平衡评估成功响应：先按轴序列出各轴结果，
// 再给出全车放行或要求复检结论。
type wheelBalanceResponse struct {
	Axles      []wheelBalanceAxleJSON `json:"axles"`
	Conclusion string                 `json:"conclusion"`
}

// 左右轮重平衡评估明确约定的字段名；其它任何拼写（含大小写变体）都视为未知字段。
const (
	keyLeftWheelLoadsKg  = "left_wheel_loads_kg"
	keyRightWheelLoadsKg = "right_wheel_loads_kg"
	keyTolerancePermille = "tolerance_permille"
)

func handleWheelBalance(c *gin.Context) {
	// 媒体类型须符合 JSON 请求契约：文本等其它类型（或缺失）直接 422，不进入评估。
	if !requireJSONContentType(c) {
		return
	}
	// 输入非法时统一走 422，且在产出任何评估结果之前拒绝，杜绝部分结果。
	req, ok := decodeWheelBalanceBody(c)
	if !ok {
		return
	}

	result, err := wheelbalance.Evaluate(req.LeftWheelLoadsKg, req.RightWheelLoadsKg, *req.TolerancePermille)
	if err != nil {
		respond422(c, err.Error())
		return
	}

	// 显式把独立领域结果映射为对外契约结构，HTTP 层不直接暴露领域类型。
	axles := make([]wheelBalanceAxleJSON, 0, len(result.Axles))
	for _, a := range result.Axles {
		axles = append(axles, wheelBalanceAxleJSON{
			TotalKg:           a.TotalKg,
			ImbalancePermille: a.ImbalancePermille,
			OverTolerance:     a.OverTolerance,
		})
	}
	c.JSON(http.StatusOK, wheelBalanceResponse{Axles: axles, Conclusion: result.Conclusion})
}

// decodeWheelBalanceBody 读取并解析左右轮重平衡评估请求：请求体大小受限、
// 未知字段（含大小写变体）拒绝、顶层必须恰好只有一个 JSON 值。
func decodeWheelBalanceBody(c *gin.Context) (*wheelBalanceRequest, bool) {
	dec := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, maxBodyBytes))
	var raw json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		respond422(c, describeWheelBalanceDecodeError(err))
		return nil, false
	}
	var extra json.RawMessage
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			respond422(c, "请求体中存在多个 JSON 值，只允许一个 JSON 对象")
			return nil, false
		}
		respond422(c, describeWheelBalanceDecodeError(err))
		return nil, false
	}
	// 字段名须与明确契约逐字符一致：encoding/json 的字段匹配不区分大小写，
	// 仅靠 DisallowUnknownFields 无法拒绝大小写变体，故先按契约名单精确扫描。
	if key := firstNonContractKey(raw,
		keyLeftWheelLoadsKg, keyRightWheelLoadsKg, keyTolerancePermille); key != "" {
		respond422(c, describeWheelBalanceDecodeError(
			errors.New("json: unknown field "+strconv.Quote(key))))
		return nil, false
	}
	strict := json.NewDecoder(bytes.NewReader(raw))
	strict.DisallowUnknownFields()
	var req wheelBalanceRequest
	if err := strict.Decode(&req); err != nil {
		respond422(c, describeWheelBalanceDecodeError(err))
		return nil, false
	}
	if req.LeftWheelLoadsKg == nil {
		respond422(c, "缺少必填字段 left_wheel_loads_kg（按车头到车尾排列的各轴左轮载荷，千克）")
		return nil, false
	}
	if req.RightWheelLoadsKg == nil {
		respond422(c, "缺少必填字段 right_wheel_loads_kg（按车头到车尾排列的各轴右轮载荷，千克）")
		return nil, false
	}
	if req.TolerancePermille == nil {
		respond422(c, "缺少必填字段 tolerance_permille（允许偏差千分比，0-1000 的整数）")
		return nil, false
	}
	return &req, true
}

// describeWheelBalanceDecodeError 描述左右轮重平衡评估请求的解码错误，
// 文案结构与其它入口一致，仅字段清单换成本入口契约。
func describeWheelBalanceDecodeError(err error) string {
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "请求体为空或 JSON 不完整，须提交包含 left_wheel_loads_kg、right_wheel_loads_kg " +
			"与 tolerance_permille 的 JSON 对象"
	case errors.As(err, new(*json.UnmarshalTypeError)),
		strings.Contains(err.Error(), "cannot unmarshal"):
		// 数组元素为 null、小数、字符串等非整数时也归入“字段类型错误”文案。
		return "字段类型错误：left_wheel_loads_kg 与 right_wheel_loads_kg 必须为整数数组（元素不得为 null），" +
			"tolerance_permille 必须为整数"
	default:
		// 含语法错误、未知字段、数字写入整型失败（如 1.5、超大数）等。
		return "JSON 解析失败：" + err.Error()
	}
}
