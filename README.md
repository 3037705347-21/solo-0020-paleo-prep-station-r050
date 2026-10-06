# 古生物修复制备台

古生物修复制备台是一个使用 Go 编写的后端基线，用于记录化石标本、审批修复方案、回填制备进度，并在全部工序完成后办理研究借阅放行。

制备负责人还维护一套**标本处置限制政策**：政策规则按基质类型与风险等级生效，规定可承受的剥离强度上限、允许的黏结材料和目标暴露比例上限。政策以草稿维护、以不可变版本发布；创建或修改修复方案时，系统按标本当前分类从最新版本取得有效限制并校验参数。

该服务只使用进程内仓储，便于在本机重复启动和检查。它不依赖数据库、远端接口或其他在线资源。

## 目录

- `cmd/paleo-prep-station`：HTTP 服务入口和监听参数。
- `cmd/workflowcheck`：通过真实 HTTP 处理器执行业务链路的本地验证命令。
- `internal/httpapi`：路由、请求解码、状态码映射和 JSON 输出。
- `internal/station`：跨领域对象的编排、事务边界、政策发布与限制评估。
- `internal/domain`：标本、方案、进度、放行记录、处置限制政策的状态与不变量。
- `internal/repository`：互斥保护的内存仓储、政策版本的追加存储和原子提交。
- `internal/contracts`：稳定错误模型和公共请求结构。

## 构建与运行

```bash
go build ./...
go run ./cmd/paleo-prep-station --addr 127.0.0.1:18133
```

服务启动后，`GET /healthz` 返回就绪状态。

## 主要接口

- `GET /specimens` 与 `POST /specimens`（登记时可提交 `risk_level`，缺省为未分类）
- `GET /specimens/{id}`
- `POST /specimens/{id}/classification`：调整标本当前基质与风险等级
- `GET /specimens/{id}/restriction`：查看当前分类命中的有效限制
- `GET /specimens/{id}/plans` 与 `POST /specimens/{id}/plans`
- `GET /plans/{id}` 与 `PUT /plans/{id}`（仅草稿可改，改完重新按最新政策评估）
- `POST /plans/{id}/approval`
- `POST /plans/{id}/progress`
- `POST /specimens/{id}/release`
- `GET /policy/draft`、`POST /policy/rules`、`DELETE /policy/rules/{matrix}/{risk}`
- `POST /policy/publications`、`GET /policy/versions`、`GET /policy/versions/{number}`

## 处置限制政策语义

- 政策规则按 `基质类型 + 风险等级(low/medium/high)` 唯一定位，负责人在唯一草稿中增删改后发布。
- 已发布版本不可变且只追加；发布生成顺序版本号和内容哈希，草稿与最新版内容相同会被拒绝（`409 policy_unchanged`）。
- 创建或修改草稿方案时，系统在同一仓储事务内按标本**当前分类**读取**最新版本**并校验剥离强度、黏结材料、目标暴露；方案固化政策版本号与分类快照。
- 政策更新不追溯：审批与写进度不再评估政策，既有方案和进度永不被新版本改写；已审批方案不可改参数。
- 确定性错误：未发布任何版本 `restriction_policy_unpublished`、标本未分类 `specimen_unclassified`、缺匹配规则 `restriction_rule_missing`、参数越界 `restriction_violation`（响应 `details` 给出字段级原因）。

## 本地验证

每条业务链路都可以独立执行：

```bash
go run ./cmd/workflowcheck --workflow register-specimen
go run ./cmd/workflowcheck --workflow approve-prep-plan
go run ./cmd/workflowcheck --workflow log-preparation
go run ./cmd/workflowcheck --workflow release-for-study
go run ./cmd/workflowcheck --workflow restriction-policy
```

这些检查会启动临时 HTTP 处理器并调用真实公共路由；`restriction-policy` 还覆盖缺失政策、越界参数、重复发布、非追溯和并发发布/评估的确定结果。

## 环境变量

项目不需要环境变量，也没有外部服务依赖。

## 测试边界

本基线按要求暂不生成单元测试、测试夹具或端到端测试文件。后续任务阶段会补充红绿验证测试，届时再替换或扩展当前的生产级链路检查。
