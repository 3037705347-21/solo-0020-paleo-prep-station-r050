# 古生物修复制备台

古生物修复制备台是一个使用 Go 编写的后端基线，用于记录化石标本、审批修复方案、回填制备进度，并在全部工序完成后办理研究借阅放行。

该服务只使用进程内仓储，便于在本机重复启动和检查。它不依赖数据库、远端接口或其他在线资源。

## 目录

- `cmd/paleo-prep-station`：HTTP 服务入口和监听参数。
- `cmd/workflowcheck`：通过真实 HTTP 处理器执行四条业务链路的本地验证命令。
- `internal/httpapi`：路由、请求解码、状态码映射和 JSON 输出。
- `internal/station`：跨领域对象的编排、事务边界和业务流程。
- `internal/domain`：标本、方案、进度和放行记录的状态与不变量。
- `internal/repository`：互斥保护的内存仓储和原子提交。
- `internal/contracts`：稳定错误模型和公共请求结构。

## 构建与运行

```bash
go build ./...
go run ./cmd/paleo-prep-station --addr 127.0.0.1:18133
```

服务启动后，`GET /healthz` 返回就绪状态。

## 主要接口

标本与方案：

- `GET /specimens` 与 `POST /specimens`
- `GET /specimens/{id}`
- `POST /specimens/{id}/reclassification`（标本当前基质/风险分类，存在未结束方案时锁定）
- `GET /specimens/{id}/plans`
- `POST /specimens/{id}/plans`
- `GET /plans/{id}`
- `PUT /plans/{id}`（仅草稿可修改参数，按当前政策重新评估）
- `POST /plans/{id}/approval`
- `POST /plans/{id}/progress`
- `POST /specimens/{id}/release`

标本处置限制政策（制备负责人维护）：

- `GET /policies/handling`（当前草稿、最新版本与全部已发布版本）
- `GET /policies/handling/versions/{version}`（读取某个不可变历史版本）
- `PUT /policies/handling/draft`（保存草稿，支持 `expected_revision` 乐观并发）
- `POST /policies/handling/publish`（冻结草稿为新版本，版本号在锁内单调分配）

## 处置限制政策

限制按「基质类型 + 风险等级（`low` / `medium` / `high`）」精确匹配，每条限制包含：

- `max_peel_strength`：可承受剥离强度上限（留空表示不限制；填写后方案必须申报 `peel_strength`）
- `allowed_adhesives`：允许使用的黏结材料白名单（空列表表示禁止任何黏结材料）
- `min_target_exposure` / `max_target_exposure`：目标暴露比例允许区间

创建或修改草稿方案时，系统在同一仓储事务内按标本**当前分类**取得**最新已发布版本**评估：

- 尚未发布任何政策 → `409 handling_policy_missing`
- 当前基质/风险等级没有对应规则 → `409 handling_rule_missing`
- 暴露比例越界 / 黏结材料不在白名单 / 剥离强度超限或缺失 → `400` 对应错误码
- 重复发布（草稿已被上一次发布消费）→ `409 no_active_policy_draft`
- 草稿并发编辑版本不符 → `409 policy_draft_revision_conflict`

方案保存通过评估的政策版本号（`policy_version`）作为不可变快照；政策新版本只影响此后的创建与草稿修改，**不会追溯改写**既有方案和进度——已审批及之后的方案连参数都不能修改。

## 本地验证

每条业务链路都可以独立执行：

```bash
go run ./cmd/workflowcheck --workflow register-specimen
go run ./cmd/workflowcheck --workflow approve-prep-plan
go run ./cmd/workflowcheck --workflow log-preparation
go run ./cmd/workflowcheck --workflow release-for-study
go run ./cmd/workflowcheck --workflow publish-handling-policy
go run ./cmd/workflowcheck --workflow enforce-handling-policy
go run ./cmd/workflowcheck --workflow concurrent-policy-evaluation
```

这些检查会启动临时 HTTP 处理器并调用真实公共路由；后三条覆盖政策发布、限制执行（含不追溯）以及创建方案与发布政策交叉并发的确定性结果。

## 环境变量

项目不需要环境变量，也没有外部服务依赖。

## 测试边界

本基线按要求暂不生成单元测试、测试夹具或端到端测试文件。后续任务阶段会补充红绿验证测试，届时再替换或扩展当前的生产级链路检查。
