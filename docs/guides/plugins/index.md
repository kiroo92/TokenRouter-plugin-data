# 插件开发手册

插件通过独立 Go 进程和共享 Vue 的前端页面扩展 TokenRouter。当前支持页面、HTTP 接口与定时任务；自定义提供商不属于本轮实现。安装要求管理员信任代码，宿主能力的权限、额度和审计不能替代操作系统或浏览器沙箱。

- [快速开始](quick_start.md)：从模板创建、运行、测试和打包。
- [SDK 参考](sdk_reference.md)：生命周期、宿主能力、KV、前端 API 与控件。
- [发布与索引](publishing.md)：五平台资产、签名目录、版本撤回与密钥配置。
- [审核标准](review.md)：代码对应关系、权限用途、资金和投递重试。

声明与协议的精确约束见[插件契约](../../interfaces/plugin_contracts.md)，部署者的恢复流程见[插件运维](../../operations/plugin_operations.md)。Go 与 Web SDK 独立采用 MIT 许可证；宿主许可证保持不变。

## 官方资源

| 资源 | 地址 |
| --- | --- |
| Go SDK v0.2.1 | [模块版本与说明](https://github.com/TokenFlux/TokenRouter/releases/tag/plugin-sdk/go/v0.2.1) |
| Web SDK 0.3.0 | [@tokenflux/tokenrouter-plugin-sdk](https://www.npmjs.com/package/@tokenflux/tokenrouter-plugin-sdk) |
| 插件模板 | [TokenFlux/tokenrouter-plugin-template](https://github.com/TokenFlux/tokenrouter-plugin-template) |
| 每日签到插件 | [TokenFlux/tr-plugin-checkin](https://github.com/TokenFlux/tr-plugin-checkin) |
| 签名索引与登记 | [TokenFlux/tokenrouter-plugins](https://github.com/TokenFlux/tokenrouter-plugins) |

SDK 与宿主分开发布。签到示例要求宿主至少为 0.6.0；SDK 发布本身不会升级或部署宿主。

## 本分发的持久目录 SDK

`kiroo92/TokenRouter-plugin-data` 的 Go SDK v0.3.0 提供 `Env.DataDir` 和支持本地文件的测试宿主，与本分发宿主 v0.7.3 配套使用。插件通过版本化 Go replace 引用此模块，命令见 [Go SDK 接入说明](../../../plugin-sdk/go/README.md)。这项后端能力不需要改动 Web SDK。
