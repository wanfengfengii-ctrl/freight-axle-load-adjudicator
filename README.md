# 夜间货车轴组复核 API（axleverify）

纯后端 HTTP API，输入按车头到车尾排列的轴载荷与相邻轴距，自动完成**轴组划分**与**超限裁决**，
消除人工目测在 1800mm 临界轴距上的分歧：**1800mm 与 1801mm 必须得到可重复、唯一的执法结论**。

- 语言 / 框架：Go 1.25 + Gin
- 裁决代码独立于 HTTP，全部规则以 testify 测试锁定（`internal/verify/verify_test.go`）
- 无数据库、无外部依赖，单容器即可运行

---

## 1. 启动方法

### Docker Compose（推荐）

```bash
# 构建并以后台方式启动 API（宿主端口默认 8080）
docker compose up -d --build

# 健康检查
curl -s http://localhost:8080/healthz
# {"status":"ok"}
```

用 `API_PORT` 覆盖宿主端口（容器内始终监听 8080）：

```bash
API_PORT=9090 docker compose up -d --build
curl -s http://localhost:9090/healthz
```

停止：

```bash
docker compose down
```

### 一次性验收服务 `verify`

Compose 中定义了名为 **`verify`** 的一次性服务：它等待 API 就绪后执行黑盒验收
（1800/1801 临界两侧结论翻转、四轴组 422 且无部分结果、相同输入响应逐字节一致、
地磅校准后超限结论翻转、偏差超 5% 拒绝、未携带地磅重量的响应逐字节回归等），
全部通过则退出码 0。

```bash
docker compose build
docker compose run --rm verify
```

### 本地直接运行（需 Go 1.25）

```bash
go test ./...                       # 运行全部 testify 测试
go run ./cmd/server                 # 默认 :8080
API_PORT=9090 go run ./cmd/server   # 指定端口
```

---

## 2. 请求契约（先读本节）

### `POST /api/v1/verify`

- `Content-Type: application/json`
- 请求体为单个 JSON 对象，未知字段一律拒绝（HTTP 422）

| 字段 | 类型 | 含义 | 约束 |
| --- | --- | --- | --- |
| `axle_loads_kg` | 整数数组 | **按车头到车尾**排列的各轴载荷，千克 | 必填；轴数 1～12；每项 1～20000 |
| `axle_spacings_mm` | 整数数组 | **相邻轴距**（第 1 项为第 1-2 轴间距，依此类推），毫米 | 必填；项数必须恰好比轴数少 1 项；每项 500～10000 |
| `scale_weight_kg` | 整数 | **选填**：收费站地磅整车重量，千克；提供时先校准各轴载荷再裁决 | 1～240000；与轴载荷合计偏差须 ≤ 5%；缺省或 `null` 时不校准 |

任一约束不满足（含字段缺失、非整数、空数组、多/少轴距项、数值越界、
地磅重量越界或偏差超 5%）都**统一返回 HTTP 422**，
响应体只有错误信息，**不会给出任何部分裁决结果**。
未携带 `scale_weight_kg` 的请求，响应与引入校准能力前逐字节一致。

### 裁决规则（已由测试锁定，非占位实现）

1. 从第 1 轴起遍历相邻轴距：
   - 轴距 **≤ 1800mm**：后轴并入当前轴组；
   - 轴距 **> 1800mm（即 1801mm 起）**：后轴开启下一个轴组。
2. 轴组限值按组内轴数取值：**单轴 10000、双轴 18000、三轴 24000 千克**。
3. 一旦形成**四轴及以上轴组**，输入非法（HTTP 422），不输出结果。
4. 组载荷与整车总载荷均为**整数求和**；载荷 **等于限值判定为合规**，仅“大于”才超限。
5. 整车限值固定为 **49000 千克**。
6. 响应中轴序号、组序号均从 **1** 开始（车头方向为 1）。
7. **地磅校准（选填）**：携带 `scale_weight_kg` 时，先计算地磅重量与轴载荷合计的差额，
   按各轴原载荷比例分摊整数千克（每轴取精确份额的向下取整部分），
   剩余整数千克按**小数部分从大到小、轴序号从小到大**逐轴补 1，
   保证校准后各轴之和严格等于地磅重量且结果可重复；
   后续轴组划分、轴组与整车裁决全部使用校准后的载荷。
8. 地磅重量与轴载荷合计的**偏差超过 5%**（恰好 5% 允许）时返回 422，只说明偏差超界。

### 成功响应（HTTP 200）

按顺序包含三部分：先列出**每个轴组**（首尾轴序号、组载荷、限值、是否超限），
再列出**整车结果**，最后是 `violations` 超限清单——组超限按组序号升序排列，
**整车超限固定置于最后**（无超限时为空数组）。

```json
{
  "groups": [
    {
      "index": 1,
      "start_axle": 1,
      "end_axle": 1,
      "axle_count": 1,
      "load_kg": 20000,
      "limit_kg": 10000,
      "over_limit": true
    }
  ],
  "vehicle": {
    "load_kg": 20000,
    "limit_kg": 49000,
    "over_limit": false
  },
  "violations": [
    { "scope": "group", "index": 1 }
  ]
}
```

整车超限时追加 `{ "scope": "vehicle" }`（无 `index` 字段）。

### 临界示例：1800 vs 1801 毫米

同样两轴各 9500kg，结论随 1mm 确定性翻转：

```bash
# 1800mm：两轴同组，双轴组 19000 > 18000，超限
curl -s -X POST localhost:8080/api/v1/verify \
  -H 'Content-Type: application/json' \
  -d '{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1800]}'
# groups[0]: axle_count=2 load=19000 limit=18000 over_limit=true
# violations: [{"scope":"group","index":1}]

# 1801mm：开启下一组，两个单轴组各 9500 <= 10000，全部合规
curl -s -X POST localhost:8080/api/v1/verify \
  -H 'Content-Type: application/json' \
  -d '{"axle_loads_kg":[9500,9500],"axle_spacings_mm":[1801]}'
# groups: 2 个单轴组，均 over_limit=false；violations: []
```

### 地磅校准示例：超限结论翻转

两轴各 9200kg、轴距 1800mm：双轴组 18400 > 18000 原本超限；
携带地磅重量 18000 后校准为 [9000, 9000]，组载荷等于限值合规：

```bash
curl -s -X POST localhost:8080/api/v1/verify \
  -H 'Content-Type: application/json' \
  -d '{"axle_loads_kg":[9200,9200],"axle_spacings_mm":[1800],"scale_weight_kg":18000}'
```

```json
{
  "groups": [
    {
      "index": 1,
      "start_axle": 1,
      "end_axle": 2,
      "axle_count": 2,
      "load_kg": 18000,
      "limit_kg": 18000,
      "over_limit": false
    }
  ],
  "vehicle": { "load_kg": 18000, "limit_kg": 49000, "over_limit": false },
  "violations": [],
  "calibration": {
    "scale_weight_kg": 18000,
    "original_total_kg": 18400,
    "difference_kg": -400,
    "calibrated_loads_kg": [9000, 9000]
  }
}
```

`calibration` 仅在携带 `scale_weight_kg` 时出现：依次给出地磅整车重量、
校准前轴载荷合计、校准差额（可正可负）与各轴校准载荷；
`groups` / `vehicle` / `violations` 均按校准后的载荷计算。

### 错误响应（HTTP 422）

```json
{ "error": "第 1 项轴距 10001 超出允许范围 500-10000 毫米" }
```

422 响应中绝不出现 `groups` / `vehicle` / `violations`。

### `GET /healthz`

返回 `200 {"status":"ok"}`，供容器探活与验收服务等待就绪使用。

---

## 3. 项目结构

```
cmd/server/main.go          HTTP 服务入口（读取 API_PORT，默认 8080）
cmd/acceptance/main.go      一次性黑盒验收程序（Compose 的 verify 服务）
internal/verify/verify.go   轴组划分与超限裁决（纯逻辑，无占位代码）
internal/verify/verify_test.go        testify 锁定全部裁决规则
internal/httpapi/handler.go           Gin 路由、请求解析与 422 处理
internal/httpapi/handler_test.go      HTTP 契约测试（200/422/字段顺序）
Dockerfile                  多阶段构建，distroless 运行镜像
docker-compose.yml          api 常驻服务 + verify 一次性验收服务
```

## 4. 规则要点速查

| 判定点 | 规则 |
| --- | --- |
| 同组边界 | 相邻轴距 ≤ 1800mm 同组；> 1800mm 另起一组 |
| 单/双/三轴组限值 | 10000 / 18000 / 24000 kg |
| 四轴及以上轴组 | 输入非法，422 |
| 等于限值 | 合规（`load > limit` 才超限） |
| 整车限值 | 49000 kg |
| 地磅重量范围 | 1～240000 kg，越界 422 |
| 地磅偏差上限 | 与轴载荷合计偏差 ≤ 5%（恰好 5% 允许），超过 422 且只说明偏差超界 |
| 差额分摊 | 按原载荷比例向下取整，余数按小数部分降序、轴序号升序逐轴补 1 |
| 超限清单顺序 | 组按序号升序，整车固定最后 |
| 非法输入 | 一律 422，仅 `{"error": ...}`，无部分结果 |
