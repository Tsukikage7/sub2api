# ai-gateway 原生发布

在仓库根目录执行：

```bash
task deploy
```

命令发布当前工作区源码，包含尚未提交的改动。它不会拉取远端、提交代码或推送 Git。

发布顺序：安装锁定的前端依赖并构建前端，使用 `backend/go.mod` 对应的 Go 工具链构建嵌入前端的 Linux AMD64 程序，上传到 SSH 别名 `ai-gateway`，备份数据库和旧程序，校验 SHA-256，原子替换 `/opt/sub2api/sub2api` 并重启 `sub2api.service`。末尾只确认 systemd 进程运行的是本次程序，不执行 HTTP、浏览器或业务验收。

前置条件：本地安装 Task、pnpm、Go、SSH、gzip 和 shasum；远端为 x86_64 Linux，SSH 连接具有 root 权限，已安装 PostgreSQL 客户端并沿用当前的 `sub2api` 数据库、服务账号及服务目录。安装 mise 时优先选择与 go.mod 完全匹配的已安装 Go，也可以通过环境变量 `GO_BIN` 指定 Go 可执行文件。可通过 `DEPLOY_HOST` 环境变量覆盖 SSH 别名。

只构建、不发布：

```bash
task build
```

构建产物为 `backend/bin/ai-gateway-linux-amd64`，该目录已被 Git 忽略。发布版本沿用上游版本号，不追加自定义构建后缀；当前为 `0.2.15`，前端自动显示为 `v0.2.15`。提交号和构建时间单独记录；工作区有改动时，提交标识带有 `-dirty`。

每次发布的备份存放在服务器 `/opt/sub2api/backups/ai-gateway-<UTC时间>-<SHA前12位>/`，包括数据库备份 `database.dump`、旧程序 `previous-binary`、环境配置和发布前的迁移列表。新程序启动可能自动执行数据库迁移；若需要回滚，应先核对迁移情况，再决定是否恢复数据库，不能只依赖旧二进制。脚本不会自动恢复数据库。
