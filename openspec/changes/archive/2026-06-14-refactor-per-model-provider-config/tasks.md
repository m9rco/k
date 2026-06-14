## 1. 解析辅助
- [x] 1.1 在 `internal/config/config.go` 新增 `commonDefaults` 结构与 `loadCommon()`,解析 `COMMON_PROVIDER`/`COMMON_BASE_URL`/`COMMON_API_KEY`,并把 `YUNWU_BASE_URL`/`YUNWU_API_KEY` 作为别名兜底(优先级:COMMON → YUNWU → 内置默认)
- [x] 1.2 新增 `resolveEndpoint(prefix, defProvider, defModel)`,实现 provider/base_url/api_key/model 四字段的"专属 → 公共 → 内置默认"逐项回退;model 不参与公共回退

## 2. 接入各后端
- [x] 2.1 给 `ImageProviderConfig` 增加 `Provider` 字段(注释标注 reserved,与 `ModelConfig` 对齐)
- [x] 2.2 用 `resolveEndpoint` 重写 `ChatPrimary`、`ChatTest`(保留 `DEEPSEEK_API_KEY` 别名)、`ImagePrimary`、`ImageBackup` 的装配,替换散落的 `envFirst`
- [x] 2.3 video 改统一命名 `VIDEO_PROVIDER`/`VIDEO_BASE_URL`/`VIDEO_API_KEY`/`VIDEO_MODEL`,`HAPPYHORSE_*` 降为别名;保持 `Configured()` 语义不变
- [x] 2.4 crawl 接入:`CRAWL_BASE_URL`(别名 `CRAWL_ENDPOINT`)无公共默认,`CRAWL_API_KEY` 回退 `COMMON_API_KEY`,`CRAWL_PROVIDER` 预留;crawl 装配签名不变,`cmd/server/main.go` 无需改动

## 3. 文档与样例
- [x] 3.1 更新 `.env.example`:以 `COMMON_*` 为主入口,旧 `YUNWU_*`/`HAPPYHORSE_*`/`CRAWL_ENDPOINT` 标注为 alias,补 `*_PROVIDER`/`*_BASE_URL` 注释
- [x] 3.2 本地 `.env` 被 protect-env 钩子保护、工具不可改;其 `YUNWU_*`/`HAPPYHORSE_*` 仍作别名生效,运行行为不变,留用户手动迁移

## 4. 测试与验证
- [x] 4.1 扩展 `internal/config/config_test.go`:覆盖「仅 COMMON 全继承」「单后端专属覆盖」「字段级部分覆盖」「provider 覆盖」
- [x] 4.2 别名链单测:`YUNWU_*` 现有部署不回归、`COMMON_*` 优先于 `YUNWU_*`、`HAPPYHORSE_*`→video、`CRAWL_ENDPOINT`→crawl base_url
- [x] 4.3 crawl 专项:`api_key` 继承公共值、端点缺省不继承公共 URL(报"未配置")
- [x] 4.4 `go test ./...` 与 `go vet ./...` 全绿
- [x] 4.5 `openspec validate refactor-per-model-provider-config --strict` 通过
