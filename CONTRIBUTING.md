# 贡献约定

Issue 与里程碑在 GitHub Project 上跟踪。动手前从最新 `main` 开分支，合入走 Pull Request。

## 分支与 PR

- 分支名建议：`issue-<编号>-简短英文`（例如 `issue-6-deploy-docs`）。
- PR 标题说清为什么；正文用 `Closes #<编号>` 或 `Fixes #<编号>` 关联 issue（合入后自动关闭）。模板只有一份：`.github/PULL_REQUEST_TEMPLATE.md`。Issue 用 `.github/ISSUE_TEMPLATE/task.yml`。
- 不要直接推 `main`。合入由仓库维护者操作。
- 不要 `--force` 推已分享的分支。

## Definition of Done

合入前对得上这些：

- [ ] 范围就是该 issue 的验收标准，没有顺手扩 scope
- [ ] 行为变更时，先改 `docs/`（契约、部署、OpenAPI/schema 若涉及）再改实现，或同一 PR 里一起改
- [ ] 相关测试通过：API 至少 `cd api && go test ./internal/httpapi`（需本机 Postgres `5433`，可用 `make up`）
- [ ] 相关手测做过（改了页面就打开对应路由；改了部署就打 `healthz`）
- [ ] 文档与仓库脚本、实际 homelab 步骤一致
- [ ] 本次若包含部署：按 [docs/release-checklist.md](docs/release-checklist.md) 勾过发布前/后
- [ ] **无明文密钥、口令、证书私钥进库**（用 Secret / `.env`；模板只放 `.env.example`）
- [ ] shorts 引擎或嵌入页改完：版本号已 bump，并按 [deploy.md](docs/deploy.md) 日常 API 路径部署（`ENV=prod` 或 `ENV=test`）、验证 `?v=`

## 密钥

不要把 `ADMIN_PASSWORD`、`JWT_SECRET`、`POSTGRES_PASSWORD`、Cloudflare token、TLS 私钥写进 markdown 或 yaml 的明文示例。需要举例时用占位符。
