# GitHub Container Registry

生产 `docker-compose.yaml` 预期从 GitHub Container Registry 拉取镜像。本文只记录仓库当前需要的使用方式。

## 镜像

Compose 文件使用后端和前端镜像：

```text
ghcr.io/<owner>/<repo>/uapi
ghcr.io/<owner>/<repo>/uapi-web
```

实际 owner/repo 以 CI 发布配置和 compose 文件为准。

## 登录 GHCR

私有镜像需要先登录：

```bash
echo <github_token> | docker login ghcr.io -u <github_user> --password-stdin
```

token 需要具备读取 package 的权限。

## 拉取和启动

```bash
cp config.example.yaml config.yaml
docker compose pull
docker compose up -d
```

升级：

```bash
docker compose pull
docker compose up -d
```

## 注意事项

- `config.yaml` 不应提交到 git。
- 缺失 secret 时程序会自动生成并写回配置；生产环境应备份该文件。
- 后端默认监听 compose 内部端口，公网入口交给宿主机反代。
- PostgreSQL 数据卷需要单独备份。
