# Bug Reproduction

## 包的性质

当前 test_model_fix 保存的是被测模型修复后的结果源码，不是初始含 Bug 源码。要复现原始缺陷，必须检出下面固定的 parent SHA；不要在当前修复结果源码上期待重新出现修复前失败。生成系统使用的可信验证补丁和完整验证日志仅在本地留存，不提交到结果分支。

## 问题现象

备件领用接口的报错分类不对，请帮我修复。

领用数量超过现有库存时，接口返回的是 400 Bad Request，前端按参数错误提示“请求格式不正确”，现场根本看不出其实是库存不够。Go 侧调用方想用 errors.Is 配合仓库导出的库存哨兵错误来判断，也判断不出来，只能去匹配报错字符串。

对照下来，领用不存在的备件返回 404、数量传 0 或负数返回 400，这两种都是对的。

期望行为：库存不足时 HTTP 返回 409 Conflict；Go 侧调用方能用 errors.Is 把这次失败识别成库存不足哨兵错误，同时不会被误判成“备件不存在”或“数量非法”；报错信息里仍然保留备件编号以及现有 / 需求数量；合法领用仍然返回 200，并保持原有的补货单联动。

仓库里已有面向公开行为的测试覆盖这几种输入。修复后请保证 go test ./... 全绿。

## 含 Bug 版本

- 仓库：11DingKing/goS12-05
- 仓库地址：https://github.com/11DingKing/goS12-05.git
- parent SHA：4558c14d0176a8b3ce7652771e9bda8a0531e811

## 复现步骤

```bash
git clone -- https://github.com/11DingKing/goS12-05.git bug-repro
cd bug-repro
git checkout --detach 4558c14d0176a8b3ce7652771e9bda8a0531e811
go test ./internal/service ./internal/httpapi -run "^TestConsumePartErrorClassification$|^TestConsumePartHTTPStatusCodes$" -count=1 -v
```

## 双架构完整错误信息

### linux/amd64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/service ./internal/httpapi -run "^TestConsumePartErrorClassification$|^TestConsumePartHTTPStatusCodes$" -count=1 -v
=== RUN   TestConsumePartErrorClassification
    consume_part_error_test.go:32: expected the shortage to be classifiable as domain.ErrInsufficientStock, got &errors.errorString{s:"consume part part-bms rejected: part part-bms: have 2, need 5: insufficient stock"} (consume part part-bms rejected: part part-bms: have 2, need 5: insufficient stock)
--- FAIL: TestConsumePartErrorClassification (0.00s)
FAIL
FAIL	batteryops/internal/service	0.033s
=== RUN   TestConsumePartHTTPStatusCodes
=== RUN   TestConsumePartHTTPStatusCodes/stock_shortage
    consume_part_status_test.go:49: expected 409, got 400: {"error":"consume part part-bms rejected: part part-bms: have 2, need 5: insufficient stock"}
=== RUN   TestConsumePartHTTPStatusCodes/non-positive_quantity
=== RUN   TestConsumePartHTTPStatusCodes/unknown_part
=== RUN   TestConsumePartHTTPStatusCodes/valid_consumption
--- FAIL: TestConsumePartHTTPStatusCodes (0.01s)
    --- FAIL: TestConsumePartHTTPStatusCodes/stock_shortage (0.00s)
    --- PASS: TestConsumePartHTTPStatusCodes/non-positive_quantity (0.00s)
    --- PASS: TestConsumePartHTTPStatusCodes/unknown_part (0.00s)
    --- PASS: TestConsumePartHTTPStatusCodes/valid_consumption (0.00s)
FAIL
FAIL	batteryops/internal/httpapi	0.047s
FAIL

```

stderr：

```text
(empty)
```

### linux/arm64

- 容器内复现预期退出码：1
- 容器内复现实际退出码：1

stdout：

```text
$ go test ./internal/service ./internal/httpapi -run "^TestConsumePartErrorClassification$|^TestConsumePartHTTPStatusCodes$" -count=1 -v
=== RUN   TestConsumePartErrorClassification
    consume_part_error_test.go:32: expected the shortage to be classifiable as domain.ErrInsufficientStock, got &errors.errorString{s:"consume part part-bms rejected: part part-bms: have 2, need 5: insufficient stock"} (consume part part-bms rejected: part part-bms: have 2, need 5: insufficient stock)
--- FAIL: TestConsumePartErrorClassification (0.00s)
FAIL
FAIL	batteryops/internal/service	0.002s
=== RUN   TestConsumePartHTTPStatusCodes
=== RUN   TestConsumePartHTTPStatusCodes/stock_shortage
    consume_part_status_test.go:49: expected 409, got 400: {"error":"consume part part-bms rejected: part part-bms: have 2, need 5: insufficient stock"}
=== RUN   TestConsumePartHTTPStatusCodes/non-positive_quantity
=== RUN   TestConsumePartHTTPStatusCodes/unknown_part
=== RUN   TestConsumePartHTTPStatusCodes/valid_consumption
--- FAIL: TestConsumePartHTTPStatusCodes (0.00s)
    --- FAIL: TestConsumePartHTTPStatusCodes/stock_shortage (0.00s)
    --- PASS: TestConsumePartHTTPStatusCodes/non-positive_quantity (0.00s)
    --- PASS: TestConsumePartHTTPStatusCodes/unknown_part (0.00s)
    --- PASS: TestConsumePartHTTPStatusCodes/valid_consumption (0.00s)
FAIL
FAIL	batteryops/internal/httpapi	0.002s
FAIL

```

stderr：

```text
(empty)
```

## 通过条件

定向验证通过：go test ./internal/service ./internal/httpapi -run '^TestConsumePartErrorClassification$|^TestConsumePartHTTPStatusCodes$' -count=1 -v
全量回归通过：go test -timeout=300s -count=1 ./...；go build ./... 与 go vet ./... 通过
linux/amd64 与 linux/arm64 两个架构均通过
库存不足 → 409 且 errors.Is 命中库存不足哨兵；备件不存在 → 404；数量非法 → 400；合法领用 → 200 且补货单联动不变
