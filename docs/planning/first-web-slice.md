# P1 首个 Web 聊天批次合同

主归属 [BASE-02](../topics/foundation.md#base-02)。评审已确认首批合同与采用决定。下表汇总跨专题验收，各能力子专题维护实施项与进度。

## 拟交付路径

部署者从干净数据目录启动单实例，按准入规则让 Alice、Bob 和 Carol 注册。Alice 和 Bob 凭完整用户名建立一对一会话。Alice 发一条带列表和代码块的 Markdown。Bob 离线后补齐错过的消息。Alice 建小群并按用户名加入 Bob 和 Carol。Carol 离开或被移出后失去访问权，重新加入后从新加入点读取历史。部署者从备份恢复数据，成员重新登录后继续使用。迁移在固定版本的合成预览 v1 库上独立验证。

<a id="p1-local-beta-gate"></a>

## P1 本地账号内测放行条件

内测面向一组已知成员，使用由部署者控制的单实例和本地账号。候选版本须通过下方矩阵的全部必验项，并保留版本或源码身份、环境、输入、结果及未覆盖范围。远程内测使用 HTTPS 实例。回环 HTTP 用于本地开发验证。

- 部署者从干净目录安装，注册默认关闭，仅按受控入口开放。直接暴露公网的入口须使用邀请码。P1 内测以关闭外部登录的配置验收。[AUTH-01](../topics/identity.md#auth-01) · [AUTH-02](../topics/identity.md#auth-02)
- 私聊、小群、Markdown、发送重试、历史分页、离线补拉及成员撤权覆盖正常、拒绝和恢复路径。桌面和窄屏能完成主流程，切换到另一测试实例时验证账号、会话和草稿隔离。[MSG-01](../topics/messaging.md#msg-01) · [MSG-02](../topics/messaging.md#msg-02) · [MSG-03](../topics/messaging.md#msg-03) · [WEB-01](../topics/clients.md#web-01)
- 用候选部署产物验证 HTTPS 入口和实时更新。从备份恢复数据后验证重新登录。在隔离环境完成固定版本的合成预览 v1 库到候选版本的迁移，并演练迁移失败后的恢复。[OPS-01](../topics/deployment.md#ops-01)
- 会话界面和内测说明应明确普通会话的服务端可见范围。日志与诊断字段、备份访问范围和撤权边界按已确认的合同验收。[P1 隐私边界](../topics/privacy.md#p1-privacy-boundary)
- 任一必验项失败或缺少目标环境证据时，保持待验收并记录复验入口。真实 Radish 登录的验收随 [AUTH-02](../topics/identity.md#auth-02) 延期处理，等稳定实例具备联调条件后再单独安排。

<a id="preview-adoption"></a>

## 预览采用清单

清单汇总 BASE-02 已确认的采用与整改范围。各行链接指向对应子专题的合同。候选版本按正式合同复验，预览结果见[探索验证记录](../status/2026-11-14-preview-verification.md)。

| 预览资产 | 采用决定 | 主归属 |
| --- | --- | --- |
| 本地账号、Cookie 会话、同源请求 | 沿用现有认证与会话机制。替换默认开放注册的策略，增加公网邀请码准入，复验 HTTPS 反代下的限流和会话 | [AUTH-01](../topics/identity.md#auth-01) |
| 私聊唯一性、群成员表和服务端授权 | 沿用私聊配对、群主按用户名加人和服务端授权。将全量群历史规则替换为按本次加入边界授权 | [MSG-01](../topics/messaging.md#msg-01) |
| REST、SSE、消息增量游标 | 沿用 `requestId` 去重、双向分页和通用同步信号。补齐收发重试、多页补拉和当前成员资格校验 | [MSG-02](../topics/messaging.md#msg-02) |
| Markdown 原文和共用渲染组件 | 沿用共用渲染、8,000 码点上限、主动打开 HTTP(S) 链接及外部图片占位 | [MSG-03](../topics/messaging.md#msg-03) |
| React 工作区与实例切换 | 沿用同源页面和地址导航。整改失败、重连与撤权后的状态，落实独立主机名、草稿隔离及普通会话隐私提示 | [WEB-01](../topics/clients.md#web-01) |
| SQLite、Dockerfile 和 Compose | 沿用单进程 SQLite 与构建部署骨架。内测从干净库安装，用固定版本的合成预览 v1 库验证迁移，备份恢复后重新登录 | [OPS-01](../topics/deployment.md#ops-01) |
| 可配置 OIDC 与合成发行方测试 | 保留现有实现和合成测试。P1 关闭外部登录，真实接入独立延期 | [AUTH-02](../topics/identity.md#auth-02) |
| Go 和 Web 检查入口 | 沿用服务端测试及 Web 类型检查和生产构建。补齐候选版本检查并接入聚合门禁 | [BASE-03](../topics/foundation.md#base-03) |

<a id="p1-start-order"></a>

## 建议开工顺序

下列顺序适用于 P1 启动后。任务表在各主子专题维护，前置任务列区分开工依赖和联合验收所需条件。

| 顺序 | 先做什么 | 本步交付 |
| --- | --- | --- |
| 1 | 固定[本地检查基线](../topics/foundation.md#base-03-01)和[合成预览 v1 样本](../topics/deployment.md#ops-01-01)，两项可并行 | 后续任务可以复跑现有检查，也有固定的迁移起点 |
| 2 | 完成[本地准入与会话](../topics/identity.md#auth-01-01)，接着推进[公网邀请码](../topics/identity.md#auth-01-02)和[私聊与成员操作](../topics/messaging.md#msg-01-01)。[禁用 OIDC 配置](../topics/identity.md#auth-02-01)同步复验，[CI 产品检查](../topics/foundation.md#base-03-02)可基于检查基线并行准备 | 服务端具备本地账号主路径和房间权限，候选检查入口可用 |
| 3 | 联合交付[群成员加入边界](../topics/messaging.md#msg-01-02)和 [SQLite 迁移](../topics/deployment.md#ops-01-02)。同步推进[消息确认与重试](../topics/messaging.md#msg-02-01)及 [Markdown](../topics/messaging.md#msg-03-01)，随后完成[分页与补拉](../topics/messaging.md#msg-02-02) | 服务端读写、历史授权和 v1 升级可联合验证 |
| 4 | 接通 [Web 登录与聊天](../topics/clients.md#web-01-01)，再收口[失败恢复与撤权状态](../topics/clients.md#web-01-02)。[镜像与 HTTPS 验收环境](../topics/deployment.md#ops-01-03)可提前准备 | 桌面和窄屏跑通主路径及拒绝、重连场景，可完整构建候选版本 |
| 5 | 用候选版本完成[独立实例切换](../topics/clients.md#web-01-03)与[备份恢复](../topics/deployment.md#ops-01-04)，按下方矩阵做联合验收 | 各子专题记录单项结果和证据链接，满足放行条件后再判定批次通过 |

<a id="p1-acceptance"></a>

## 首轮验收矩阵

| 场景 | 预期结果 | 实施与验收归属 |
| --- | --- | --- |
| 注册、退出和重登 | 默认拒绝注册。受控入口可按配置开放，公网开放时仅有效邀请码可建号。关闭注册后已有账号仍可登录。退出或到期后的会话被拒绝，外部登录禁用 | [AUTH-01](../topics/identity.md#auth-01) · [AUTH-02](../topics/identity.md#auth-02) |
| 两端发私信并重试 | 同一对账号打开同一会话。重试不会生成第二条消息，重试正文冲突可识别 | [MSG-01](../topics/messaging.md#msg-01) · [MSG-02](../topics/messaging.md#msg-02) |
| 窗口离线、新消息与历史分页交错 | 断网期间累积超过一页的消息，恢复途中继续发送。重连后补齐全部应获得的消息，向前翻页和增量补拉交错时顺序稳定、无漏页、无重复 | [MSG-02](../topics/messaging.md#msg-02) |
| 加入、移除和重新加入 | 群主可按用户名加人，历史只返回本次加入后产生的消息。退出或移除后停止访问，已打开页面清理会话。重新加入后，前一次在群期间及离群期间的消息都在当前授权范围之外。其他会话的通用同步信号仍可到达 | [MSG-01](../topics/messaging.md#msg-01) · [WEB-01](../topics/clients.md#web-01) |
| Markdown、桌面与窄屏 | 同一条消息在预览和收方渲染一致，创建会话与收发流程能完成，未知链接与外部内容按合同处理 | [MSG-03](../topics/messaging.md#msg-03) · [WEB-01](../topics/clients.md#web-01) |
| 两个实例间切换 | 同一浏览器在两个独立 HTTPS 主机名间往返，凭据、列表与草稿均保持实例隔离 | [WEB-01](../topics/clients.md#web-01) |
| 干净安装与 HTTPS 实时收发 | 按部署说明使用完整构建的候选产物安装，经 HTTPS 复现实例健康检查、普通账号收发与断线恢复 | [OPS-01](../topics/deployment.md#ops-01) · [MSG-02](../topics/messaging.md#msg-02) |
| 备份、迁移和恢复 | 从干净安装的数据备份恢复到新目录，核对账号、成员和消息，旧会话失效并可重新登录。验证固定版本的合成预览 v1 库迁移至候选版本，并演练迁移失败后的备份恢复 | [OPS-01](../topics/deployment.md#ops-01) · [AUTH-01](../topics/identity.md#auth-01) |
| 普通会话的内容与访问说明 | 用户能辨认服务端可读的会话。实时信号不携带正文或会话标识，日志与备份按隐私边界验收 | [WEB-01](../topics/clients.md#web-01) · [MSG-02](../topics/messaging.md#msg-02) · [OPS-01](../topics/deployment.md#ops-01) |
| 候选版本检查 | 从干净检出完成 Go 测试与 `go vet`、Web 类型检查与生产构建，并验证 `Candidate Quality` 汇总结果。候选镜像另外记录完整构建证据 | [BASE-03](../topics/foundation.md#base-03) |

批次结论须关联实际运行版本、隔离数据、环境和测试结果。P1 候选提交 `8809752` 已按本矩阵通过本机联合验收，随 PR #3 合入稳定主线。验收版本、环境、输入和实测范围见[联合验收记录](../records/2026-11-15-p1-local-joint-acceptance.md)。
