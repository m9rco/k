# Change: 每模型独立 token/url/provider 配置(缺省回退公共)

## Why
当前所有模型后端实际共用单一供应商(yunwu.ai),公共凭证硬编码在 `YUNWU_API_KEY` / `YUNWU_BASE_URL` 上。配置层虽已用 `envFirst` 给 chat/image/video 做了"专属 → 公共"回退,但机制不统一:

- crawl 完全没有公共回退(`CRAWL_ENDPOINT` / `CRAWL_API_KEY` 各自独立)。
- video 走 `HAPPYHORSE_*` 专名,不符合统一命名。
- 公共变量名 `YUNWU_*` 与"未来某些模型会换供应商"的目标语义冲突。
- provider 维度虽已可 per-model 配(`CHAT_*_PROVIDER`),但 image/video 没有,且未文档化。

目标:让每个模型后端都能**独立配置 `provider / base_url / api_key`**,任一项缺省则回退到一组**语义中立的公共默认**,使后续单独把某个模型换到新供应商时,只需补该模型的专属变量、零改其他配置与代码。

## What Changes
- 引入语义中立的公共回退三元组:`COMMON_PROVIDER` / `COMMON_BASE_URL` / `COMMON_API_KEY`。`YUNWU_*` 保留为向后兼容别名(优先级低于 `COMMON_*`、高于内置默认)。
- 为**全部模型后端**(chat 主/测、image 主/备、video)统一支持 per-model 覆盖三元组 `<PREFIX>_PROVIDER` / `<PREFIX>_BASE_URL` / `<PREFIX>_API_KEY`,任一缺省回退公共值。`<PREFIX>_MODEL` 维持现状(模型 id 无公共回退,各后端有各自内置默认)。
- 将 **crawl** 纳入同一回退机制:`CRAWL_PROVIDER`(预留)/ `CRAWL_BASE_URL` / `CRAWL_API_KEY`,api_key 缺省回退 `COMMON_API_KEY`;base_url 无公共默认(crawl 端点形态与模型 API 不同,缺省即"未配置")。保留 `CRAWL_ENDPOINT` 作为 `CRAWL_BASE_URL` 的兼容别名。
- 给 `ImageProviderConfig` 增加 `Provider` 字段(与 `ModelConfig` 对齐),video 改用统一命名 `VIDEO_PROVIDER` / `VIDEO_BASE_URL` / `VIDEO_API_KEY` / `VIDEO_MODEL`,`HAPPYHORSE_*` 降级为兼容别名。
- 抽出统一的解析辅助 `resolveEndpoint(prefix, defaults)`,集中实现"专属 → 公共 → 内置默认"的三层回退,替换散落的 `envFirst` 调用,保证所有后端行为一致。
- 更新 `.env.example` 与 `config_test.go`,覆盖每个后端的三层回退与别名兼容。

不在本次范围(Non-Goals):
- COS 对象存储凭证(`COS_*` 为多字段、语义与模型 token/url 不同)不纳入本机制。
- 不改变运行时模型选择逻辑(`USE_TEST_MODEL`)、不改 provider 协议实现(anthropic/openai 分支)。
- 不引入配置文件(仍走环境变量),不做加密/鉴权(项目约束:小团队内部、硬编码可接受)。

## Impact
- Affected specs: `provider-configuration`(新增 capability)
- Affected code:
  - `internal/config/config.go`(核心:新增 `resolveEndpoint`,重写 chat/image/video/crawl 的装配)
  - `internal/config/config_test.go`(回退与别名用例)
  - `internal/crawl/source.go`(`NewHTTPSource` 接收统一三元组或保持签名、由 main 适配)
  - `cmd/server/main.go`(crawl 装配处)
  - `.env.example`、`.env`(新增 `COMMON_*`,标注别名)
- 向后兼容:既有部署只设了 `YUNWU_*` / `HAPPYHORSE_*` / `CRAWL_ENDPOINT` 的,行为不变。
