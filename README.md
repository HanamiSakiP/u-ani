# uani

`uani` 是 Go CLI 

- 仓库：https://github.com/HanamiSakiP/u-ani
- 当前版本：`1.0.1`

## 构建

需要 Go 1.26+：

```bash
go build -o uani ./cmd/uani
```

也可以直接运行：

```bash
go run ./cmd/uani --help
```

### 通过 go install 安装

```bash
go install github.com/HanamiSakiP/u-ani/cmd/uani@latest
uani --version
```

在当前源码目录中也可以本地安装：

```bash
go install ./cmd/uani
```

如果 `~/go/bin` 不在 `PATH` 中，需要先加入：

```bash
export PATH="$HOME/go/bin:$PATH"
```

> 首次安装需要 Go 1.26+。使用 `@latest` 前请确认仓库已经推送了带 `go.mod` 的版本标签或分支。

### 自升级

`uani upgrade` 会通过 Go 工具链安装最新版本并迁移本地数据库：

```bash
uani upgrade              # 检查最新版本，执行 go install ...@latest，再迁移数据库
uani upgrade --check      # 只检查最新版本，不安装
uani upgrade --force      # 已是最新版本时也重新安装
uani upgrade --version v1.2.3   # 安装指定版本
uani upgrade --local      # 使用本地源码构建并安装
uani upgrade --local --no-sync   # 不联网同步源码，直接使用现有 ${UANI_PATH}/source
uani upgrade --local --source-dir ./   # 使用指定源码目录（例如当前源码仓库）
uani upgrade --no-install # 跳过安装，只迁移数据库
```

远程升级成功后会同步一份源码到 `${UANI_PATH}/source`（默认 `~/.uani/source`），后续可以用 `--local` 从这份源码离线构建。源码目录中会写入 `.uani-source-version` 记录版本。执行 `uani install` 时也会尝试同步源码，可用 `--no-source` 跳过。Web 升级时也可以选择「远程 go install」或「本地源码」。

检查更新会优先读取 GitHub 仓库的最新 Release / Tag，因此新版本推送后会立即生效；GitHub 不可达时自动回退到 Go 模块代理。同步源码优先使用 Go 模块缓存，失败时直接下载 GitHub 对应版本的源码归档（tar.gz），不依赖模块代理是否已经收录该版本。GitHub API 触发限流时可设置 `UANI_GITHUB_TOKEN`（或 `GITHUB_TOKEN` / `GH_TOKEN`）提升额度。

也可以在 Web UI 的「设置 → 运行信息与升级」中操作：检查更新、指定版本、选择「远程 go install」或「本地源码」、同步源码。升级使用：

```bash
go install github.com/HanamiSakiP/u-ani/cmd/uani@latest
```

因此需要本机安装 Go，且 `GOBIN`（默认 `$GOPATH/bin`）中的 `uani` 是当前运行的二进制。升级完成后需要重启 `uani http` 才会加载新版本。

可选依赖：使用「用 Kazumi 下载番剧」功能时建议安装 `yt-dlp`（推荐）或 `ffmpeg`，程序会自动检测。Python 不是运行依赖，仅在需要兼容旧版 BGmi Python 脚本时才会使用。

## 快速开始

首次启动任一命令时，`uani` 会自动创建数据目录和默认配置文件，无需先执行 `install`。

```bash
# 选择一个数据源（bangumi_moe / mikan_project / dmhy）
./uani source mikan_project

# 更新番剧日历
./uani cal --update

# 搜索并订阅
./uani search "葬送的芙莉莲"
./uani add "葬送的芙莉莲" --episode 0

# 同步 Bangumi 追番（需先 uani sync login <token>）
./uani sync

# 下载新一集
./uani update

# 查看订阅 / 下载记录
./uani list
./uani seen mark "葬送的芙莉莲" 1
```

如需要初始化数据库、crontab 和配置，可执行：

```bash
./uani install --no-web
```

内置 Web 前端随二进制一起发布，不再需要下载外部前端资源。

默认数据目录为 `~/.uani`，可通过环境变量 `UANI_PATH` 指定：

```bash
UANI_PATH=/path/to/uani ./uani install --no-web
```

配置文件位于 `${UANI_PATH}/config.toml`，SQLite 数据库位于 `${UANI_PATH}/bangumi.db`。
启动时会自动创建：

- `${UANI_PATH}` 及上述各目录
- `save_path`、`cache_path`、`tmp_path`、`log_path`、`script_path`、`tools_path`、`front_static_path`
- `db_path` 所在目录
- 默认 `config.toml`（仅当文件不存在时）

## 命令

```text
uani install                 初始化目录、数据库、crontab 和配置
uani upgrade                 通过 go install 升级 uani 并迁移数据库
uani source <source>         切换数据源
uani config print            输出配置文件
uani config get <keys...>    读取配置项
uani config set <keys...>    修改配置项（--value）
uani search <keyword>        搜索种子
uani add <names...>          订阅番剧（支持 --bgm <bgm.tv 条目>）
uani cover                   下载所有番剧封面到本地
uani kazumi catalog          浏览 Kazumi 规则仓库
uani kazumi install <names>  安装指定 Kazumi 规则
uani kazumi list             查看已安装规则
uani kazumi update [names]   更新规则（默认全部）
uani kazumi remove <names>   删除规则
uani kazumi repository [url] 查看/设置规则仓库
uani kazumi acceleration     查看/设置番剧条目、规则仓库、图片加速
uani kazumi mirror [url]     查看/添加/切换/测试 Bangumi API 镜像
uani kazumi search <rule> <keyword>        用 Kazumi 规则搜索番剧
uani kazumi chapters <rule> <source>       查看剧集线路
uani kazumi download <rule> <name> <ep> <source>  下载剧集（也支持 --rule/--name/-e/--source）
uani kazumi downloads        查看 Kazumi 下载任务
uani sync                    同步 Bangumi 追番到本地订阅
uani sync login <token>      验证并保存 Bangumi Access Token
uani sync logout             清除已保存的 Bangumi 账号
uani sync status             查看追番同步状态
uani sync types [types...]   查看/设置要同步的收藏分类
uani delete <names...>       取消订阅（同时清除 Bangumi 收藏状态）
uani list                    查看订阅列表
uani filter <name>           设置字幕组 / 包含 / 排除 / 正则过滤
uani cal                     查看每周番剧日历
uani fetch <name>            查看某部番剧当前可下载剧集
uani update [names...]       获取并下载新剧集
uani history                 查看订阅历史
uani gen nginx.conf          生成 nginx 配置
uani debug info              查看运行时信息
uani http|web|serve          启动 HTTP API、Web 前端和 MCP 服务（可用 --no-web / --no-mcp / --api-only）
uani stop|kill-web           停止正在运行的 Web 服务（默认 8888 端口）
uani postprocess             处理已完成的下载并移动到格式化路径
uani seen mark <name> <ep>   标记剧集为已下载
uani seen forget <name> <ep> 取消已下载记录（下次 update 重新下载）
uani completion <shell>      生成 shell 补全脚本
```

`search` 支持 `--regex-filter`、`--dupe`、`--min-episode`、`--max-episode`、`--tag`、`--subtitle`、`--download`、`--download-as`。
`add` 支持 `--episode`、`--latest`、`--season`、`--save-path`、`--episode-offset`、`--display-name`，以及 `--bgm <id|URL>`。

### bgm.tv 订阅

可以用 bgm.tv 条目 ID 或链接添加订阅：

```bash
uani add --bgm 253
uani add --bgm https://bgm.tv/subject/253
```

- 如果 bgm.tv 条目能匹配当前数据源中的番剧，会按普通订阅处理，并补齐缺失封面。
- 如果数据源中没有对应条目，会创建“手动订阅”：使用 bgm.tv 的中文名/原名/别名、封面和星期信息，`update` 时在当前数据源中按标题和别名搜索种子。
- 手动订阅同样支持 `--episode`、`--latest`、`--season`、`--episode-offset`、`--display-name` 以及字幕组/包含/排除/正则过滤。
- 手动订阅依赖种子站标题匹配；如果数据源没有收录该番剧，或标题差异过大，可能无法下载。可通过 `uani filter` 调整过滤条件。
- HTTP 管理 API 的 `add` 也接受 `bgm_id` 或 `bgm_url`；MCP 的 `add` tool 接受 `bgm_id`/`bgm_url`。

### Bangumi 追番同步（同步订阅）

uani 支持像 Kazumi 的「追番」功能一样，把 Bangumi 账号里的收藏同步成本地订阅：

```bash
# 在 https://next.bgm.tv/demo/access-token 创建个人令牌
uani sync login <access_token>
uani sync status

# 把全部收藏同步为订阅
uani sync

# 先看会新增哪些订阅
uani sync --dry-run

# 同步「在看」和「想看」
uani sync types watching,wish
uani sync
```

- 默认同步全部收藏分类（`wish` 想看、`watched` 看过、`watching` 在看、`on_hold` 搁置、`dropped` 抛弃），可用 `uani sync types` 或 `uani sync --types` 选择需要的分类或 `all`。
- 同步时会读取 Bangumi 收藏的 `ep_status`，把已经看过的集数标记为已下载，从下一集开始下载；也可以使用 `--episode N` 或 `--latest` 覆盖。
- 已经在 uani 中订阅的番剧不会重复添加；数据源中能匹配到的番剧按普通订阅处理，匹配不到时创建手动订阅（使用标题和别名搜索种子）。
- 设置 `bangumi.sync_enabled = true` 后，每次执行 `uani update` 会先自动同步追番列表：

```bash
uani config set bangumi sync_enabled --value true
```

- Token 保存在 `${UANI_PATH}/config.toml` 的 `[bangumi]` 段中，请妥善保管；`uani sync logout` 会清除账号并关闭自动同步。

### Kazumi 规则仓库与访问加速

uani 内置 Kazumi 规则仓库管理。默认使用官方仓库
`https://raw.githubusercontent.com/Predidit/KazumiRules/main/`，支持浏览目录并只安装指定规则：

```bash
uani kazumi catalog AGE              # 搜索规则
uani kazumi install AGE mutefun      # 安装指定规则
uani kazumi install --all            # 安装仓库中的全部规则
uani kazumi list                     # 查看已安装规则
uani kazumi update                   # 更新全部已安装规则
uani kazumi remove AGE               # 删除规则
```

规则保存在 `${UANI_PATH}/kazumi/rules/*.json`，原始 JSON 内容与仓库保持一致。也可以使用自定义 raw 仓库：

```bash
uani kazumi repository https://raw.githubusercontent.com/<user>/<repo>/main/
uani kazumi repository --default
```

访问加速选项与 Kazumi 官网一致：

```bash
uani kazumi acceleration                              # 查看全部
uani kazumi acceleration bangumi mirror                # 番剧条目：direct / ech / mirror
uani kazumi acceleration rules ghProxy                 # 规则仓库：ghProxy / ghfast / gitcode / direct
uani kazumi acceleration image ech                     # 图片：direct / ech / mirror
```

- **番剧条目加速**：`direct` 访问官方 `next.bgm.tv` 接口，`mirror` 使用当前选中的 Bangumi 镜像，`ech` 使用 ECH 优先的 TLS 连接（不支持时会自动回退）。
- **规则仓库加速**：`ghProxy`（默认，`gh-proxy.org`）、`ghfast`（`ghfast.top`）、`gitcode`（`raw.gitcode.com` 镜像）或 `direct`；镜像只作用于官方 KazumiRules 仓库。
- **图片加速**：`mirror` 将 bgm.tv 图片交给 `https://wsrv.nl/`，`ech` 优先使用 ECH，`direct` 使用原图地址。封面下载和内置 Web UI 的 Kazumi 时间表 / 推荐 / 详情图片都会通过 `/api/image` 代理应用该设置，失败时自动回退到镜像。
- 自定义代理仍使用全局 `proxy` 配置。

Bangumi 镜像支持添加多个并随时切换当前使用的地址：

```bash
uani kazumi mirror                          # 查看镜像列表，* 表示当前使用
uani kazumi mirror https://api.kazumi.fyi   # 添加并切换
uani kazumi mirror --add https://api.bgmapi.com
uani kazumi mirror --remove https://example.com
uani kazumi mirror --test                   # 测试当前镜像
uani kazumi mirror --test https://api.bgmapi.com
```

- 搜索会优先使用当前镜像，并并发尝试其他已配置镜像和内置 `https://api.bgmapi.com` 兜底。
- 默认使用 `https://next.bangumi.lat` 作为 p1 接口（时间表、详情、吐槽）的镜像源；`v0` 角色 / 关联和搜索会自动回退到 `https://api.kazumi.fyi` 等兼容镜像。吐槽额外保留公共兜底，所有来源都不可用时会返回可重试的「暂不可用」状态，而不是让页面直接报错。
- 设置页和 Kazumi Files → 网络设置中同样可以添加、删除、测试和切换镜像；全部删除后恢复内置默认镜像。

HTTP 管理 API 提供 `/api/admin/kazumi/catalog`、`/api/admin/kazumi/rules`、`/api/admin/kazumi/rules/update`、`/api/admin/kazumi/settings`；MCP 新增 `kazumi_catalog`、`kazumi_list`、`kazumi_install`、`kazumi_update`、`kazumi_remove`、`kazumi_get_network`、`kazumi_set_network`。

Bangumi 追番同步提供 `/api/admin/bangumi/token`（POST/DELETE）、`/api/admin/bangumi/sync`（GET/POST/PATCH）以及 MCP tools `bangumi_sync_status`、`bangumi_sync`、`bangumi_set_token`、`bangumi_configure_sync`、`bangumi_logout`。

### 用 Kazumi 下载番剧

`uani http` 的内置前端包含 **Kazumi** 页面，可以直接使用已安装的 Kazumi 规则搜索番剧、选择线路和剧集并下载：

1. 在「Kazumi」页面安装规则（支持 XPath 与 API 规则）；
2. 在任意番剧详情中点击 **开始观看**。如果该番剧在 Kazumi Files 或本地目录中已有下载完成的剧集，会直接列出这些本地集数并播放；需要在线搜索时再点击 **在线搜索更多来源**。没有本地剧集时才会自动用所有已安装规则搜索番剧并列出播放来源；
3. 选择来源后加载剧集线路，每集都可以选择 **直接播放** 或 **下载**；
   未匹配到结果时可以点击 Bangumi 别名重新搜索；
4. 在「Kazumi Files」页面查看进度、播放或删除文件。

下载器按以下顺序自动选择，并在失败时自动尝试下一种：

- `yt-dlp`（推荐，可解析大多数播放页）；
- `ffmpeg`（已解析到 m3u8/mp4 直链时可用）；
- 内置 HLS 下载器（m3u8 主/媒体播放列表、AES-128 加密分片、fMP4）；
- 内置 HTTP 下载（mp4 直链）。

同时支持常见 MacCMS `player_aaaa` 播放器配置、加密直链和 player.js 播放页解析；对 LMM 等使用 `vxdev` 播放器的站点，会复现其 `vxdev.php` 签名请求并直接取得真实 mp4/m3u8 地址，行为与 Kazumi 官网播放链路一致。对于 7sefun 这类 `player.js` 内嵌广告播放器的站点，会优先跟随解码后的真实播放页，避免把广告 mp4 当成正片。下载失败的任务可以在「Kazumi Files」中点击 **重试** 或 **删除**；下载成功后会清理同一集遗留的失败缓存/临时文件。

`giriGiriLove` 使用 API 规则，不包含验证码配置；此前搜索失败是因为 API 搜索返回的 `vod_id` 被错误拼接成 URL，现已修复，并增加了网络重试。对于确实启用了 WebView 验证码的规则，程序会明确提示需要验证码/登录 Cookie。

文件保存在 `save_path/kazumi/<番剧名>/`，通过 `/kazumi-files/` 提供播放。若站点需要验证码，请先在浏览器中完成验证；部分站点仍可能需要登录 Cookie。

命令行下载也支持更适合复制长链接的写法：

```bash
uani kazumi download --rule AGE --name "葬送的芙莉莲" -e 1 --source "https://..."
```

相关 API：

- `POST /api/admin/kazumi/search`：`{rule, keyword}`
- `POST /api/admin/kazumi/chapters`：`{rule, source}`
- `POST /api/admin/kazumi/download`：`{rule, name, episode, title, source}`
- `GET /api/admin/kazumi/downloads`、`DELETE /api/admin/kazumi/downloads/<id>`（取消）
- `POST /api/admin/kazumi/downloads/<id>/retry`（重试）
- `DELETE /api/admin/kazumi/downloads/<id>?delete=1`（删除任务及文件）
- `GET /api/admin/kazumi/files`、`DELETE /api/admin/kazumi/files?path=...`
- `GET/PUT /api/admin/kazumi/rules/<name>/file`：读取或保存规则 JSON
- `GET /api/image?url=...`：Bangumi 图片代理，自动应用「图片加速」设置（镜像 / ECH / 直连）

### 内置 Web 前端

前端已重写为内置的轻量单页应用，启动 HTTP 服务后直接访问 `/` 即可，无需安装或构建。界面已适配手机端：侧边栏抽屉、顶部操作区自动换行、卡片两列布局，详情弹窗和设置表单在窄屏下自动收敛为单列，并处理了刘海屏安全区域。主要功能：

- **追番**：按 Bangumi 收藏状态筛选（全部 / 在看 / 想看 / 搁置 / 看过 / 抛弃）；**已缓存** 只显示 Kazumi Files 中确实有已完成视频的番剧；卡片显示实际已下载集数，点击 **开始观看** 直接播放本地剧集，不再先搜索在线来源；支持名称搜索、更新检查、添加 / 删除订阅、**更新追番完整信息**（概述 / 角色 / 关联 / 制作人员 / 吐槽文字缓存，图片仅缓存封面、角色和关联）、**追番信息本地导入 / 导出**（可选择仅 JSON，或导出含已缓存封面和番剧文字信息的 ZIP；导入时自动识别 JSON / ZIP）、**下载库导入 / 导出**（导出为 ZIP 压缩包，内含真实视频文件、已缓存封面和番剧信息；导入时会补全缺失的已下载追番条目）、**缓存本地导出 / 导入**（番剧详情与吐槽文字，以及 Kazumi 时间表 / 推荐缓存；不修改追番、订阅和已下载文件）；
- **放送日历**：按星期展示当季番剧；
- **Kazumi 时间表**：Bangumi 当季番剧放送时间表，可按星期切换，点击卡片查看详情；切换星期直接使用本地缓存；支持时间表 / 推荐缓存的单独 **导出 / 导入**（ZIP 压缩包，含已缓存封面和番剧文字信息）；
- **Kazumi 推荐**：与原版一致的「热门番组 + 日常 / 原创 / 校园 / 搞笑 / 奇幻 / 百合 / 恋爱 / 悬疑 / 热血 / 后宫 / 机战 / 轻改 / 偶像 / 治愈 / 异世界」分类；搜索会查询 Bangumi 全站，返回后立即预加载结果封面，在线搜索不可用时回退本地时间表匹配并提示；**更新缓存**会同时预取所有分类，进入推荐页也会后台预热其余分类，之后切换分类无需等待网络；封面统一走图片代理并自动在加速模式与镜像之间快速切换；
- **Kazumi Files**：下载任务按番剧合并显示，带封面、进度状态汇总和可折叠的剧集列表；媒体文件按同名番剧合并，点击展开选集后播放 / 打开 / 删除；另含已安装规则、规则仓库、网络设置、规则文件查看 / 编辑；
- **设置**：管理令牌、运行信息与升级，以及合并为单一面板的「同步与镜像源」；面板内通过按钮切换 **Bangumi 同步 / WebDAV 同步 / 镜像源**，镜像可添加、删除、测试并切换当前使用；MCP 服务可以在运行信息中直接启用/关闭，立即生效并写回 `config.toml`。

详情弹窗和图片预览都会限制在视口内，图片使用自适应缩放，不需要左右滚动条。图片预览支持「上一张 / 下一张」按钮，也可以使用键盘左右方向键切换。详情页支持通过按钮切换：

- **概述**：封面、评分、排名、放送日期、标签、简介、Bangumi infobox 详细信息；
- **吐槽**：用户短评、评分、时间；
- **角色**：角色图片、关系、声优；角色图片和制作人员头像可点击放大查看；
- **关联**：关联条目，可继续点开查看；
- **制作人员**：导演、脚本、原作等制作信息。

「追番」和「放送日历」的卡片可以直接点开同一个详情弹窗；弹窗顶部提供「上一部 / 下一部」按钮，可在当前追番筛选结果或当前日历日内连续切换。时间表 / 推荐详情会自动预取相邻条目，更新缓存后上一部 / 下一部无需等待网络。本地视频播放器会显示「已缓存 N 集」，并列出可点击的本地剧集，点击即可切换播放。封面会下载到本地 `.cover`，详情数据首次读取后缓存到数据库，之后可离线预览。未自动匹配到 Bangumi 条目时，可在弹窗中手动输入 bgm.tv 条目 ID 或链接进行关联。已经订阅的番剧会在时间表 / 推荐 / 日历详情中自动识别，显示「订阅中」和「取消订阅」，不会再出现「添加订阅」。取消订阅时会同时清除该条目在 Bangumi 的收藏状态（想看 / 看过 / 在看 / 搁置 / 抛弃）；如果未配置 Access Token，则至少清除本地缓存并提示远程状态未清理。

**本地播放器**：播放本地剧集时，播放器顶部会出现 **本地播放器** 按钮，点击后由 `uani http` 所在机器启动外部播放器（例如 mpv / VLC / PotPlayer / IINA）。默认会自动探测常见播放器；也可以显式配置：

```toml
[player]
command = "mpv --fullscreen {path}"   # {path} 会替换为本地视频路径；留空则自动探测
```

Web 页面只把 `save_path` 或 Kazumi Files 目录内的媒体文件交给播放器，并会拒绝目录穿越和 HTTP 链接。打开本地播放器或「新窗口打开」时会自动暂停当前正在播放的视频。加载弹窗提供 **取消** 按钮，可中止正在进行的请求和等待。

详情弹窗中的 Bangumi 收藏按钮支持切换 **想看 / 看过 / 在看 / 搁置 / 抛弃**，与 Bangumi 官方收藏状态一致；需要先在设置中连接 Bangumi Access Token。在「追番」页面以外（放送日历、Kazumi 时间表、推荐、关联条目等）切换收藏状态时，会自动把该条目添加到 uani 订阅；在「追番」页面内只同步收藏状态。

管理操作需要输入 `[http] admin_token`，令牌仅保存在浏览器 localStorage。旧的外部前端仍可通过 `http.serve_static_files = true` 且 `front_static_path/index.html` 存在时覆盖内置页面。

### WebDAV 同步

WebDAV 用于在多个 uani 实例之间同步追番收藏状态，使用与 Kazumi 相同的 `/kazumiSync` 目录：

```bash
uani config set webdav url --value https://dav.example.com/remote.php/dav/files/user
uani config set webdav username --value user
uani config set webdav password --value app-password
```

也可以在「设置」页面填写 WebDAV 地址、用户名、密码后点击「立即同步」。支持两种方向：

- **云端 → 本地**（默认）：用云端收藏覆盖本地
- **本地 → 云端**：用本地收藏覆盖云端

同步快照保存在 `/kazumiSync/uani-collectibles.json`，按 `bangumi_id` 保存收藏状态。

### Bangumi 镜像

Bangumi API 镜像可以在设置页或 Kazumi Files → 网络设置中管理，也可以使用 CLI：

```bash
uani kazumi mirror                                    # 查看列表
uani kazumi mirror https://api.kazumi.fyi             # 添加并切换
uani kazumi mirror --add https://api.bgmapi.com       # 只添加
uani kazumi mirror --remove https://example.com       # 删除
uani kazumi mirror --test                             # 测试当前镜像
```

镜像列表保存在 `[kazumi]` 段，`bangumi_mirror` 表示当前选择的镜像，`bangumi_mirrors` 是候选列表：

```toml
[kazumi]
bangumi_mirror = "https://api.kazumi.fyi"   # 当前使用
bangumi_mirrors = [
  "https://api.kazumi.fyi",
  "https://api.bgmapi.com",
]
```

搜索、详情和吐槽会优先使用当前镜像，并在失败时并发尝试其余镜像；默认镜像列表为 `https://next.bangumi.lat`（p1 时间表 / 详情 / 吐槽）、`https://api.kazumi.fyi`（v0 角色 / 关联 / 推荐）和 `https://api.bgmapi.com`（无需签名的 v0 搜索结果兜底）。不支持的路径会自动切换到兼容镜像。镜像测试会同时检查 `/p1/calendar`、旧版搜索和 `/v0/subjects/253`，避免把返回 HTTP 200 错误 JSON 的地址误判为可用。

## 数据源

- `bangumi_moe`：https://bangumi.moe/
- `mikan_project`：https://mikanani.me/
- `dmhy`：https://share.dmhy.org/

切换数据源会清空番剧、订阅、下载记录和字幕组缓存，与上游 `bgmi source` 行为一致。

## 下载器

配置项 `download_delegate` 支持：

- `aria2-rpc`
- `transmission-rpc`
- `qbittorrent-webapi`
- `deluge-rpc`
- `noop`（仅记录，不实际下载，适合测试）

RPC 地址、端口、用户名、密码等配置项与原版一致。


```python
from bgmi.script import ScriptBase
from bgmi.utils import parse_episode
```

脚本中的 `ScriptBase.Model`（`bangumi_name`、`update_time`、`cover`、`due_date`、`source`、`bangumi_id`、`subtitle_list`、`max_page`）以及 `get_download_url()` 均按原版方式工作。

- Python 解释器可通过 `UANI_PYTHON` 指定（兼容旧的 `BGMI_PYTHON`）。
- 如果环境中安装了 `anime_episode_parser`，shim 会优先使用它；否则使用内置回退解析。

## HTTP / Web / MCP

Go 版本内置了与上游 `bgmi_http` 对应的 HTTP 服务：

```bash
uani http --address 0.0.0.0 --port 8888
# 简写
uani http -a 0.0.0.0 -p 8888

# 不启动 MCP
uani http --no-mcp

# 不启动 Web UI，只保留 JSON API 和媒体文件
uani http --no-web

# 只启动 API（Web 和 MCP 都关闭）
uani http --api-only

# 停止正在运行的 Web 服务（默认 8888 端口）
uani stop
uani web stop
uani web stop -p 9999
```

默认监听 `0.0.0.0:8888`，同时启动 JSON API、Web UI 和 MCP。启动前会自动创建数据目录、配置文件和数据库。如果端口已被旧的 `uani` 实例占用，启动时会先结束旧进程再重新监听，无需手动 `kill`；也可以在另一个终端用 `uani stop`（或 `uani web stop`）单独停止服务。

需要永久关闭其中某个服务时，可以写配置：

```bash
uani config set http disable_web --value true
uani config set http disable_mcp --value true
```

再次设为 `false` 即可恢复。服务启动后会在后台补齐缺失封面；也可以随时手动执行：

```bash
uani cover
```

封面保存在 `cache_path/covers/`，Web 前端通过 `/covers/...` 访问（旧路径 `/bangumi/.cover/...` 仍兼容）。

- Web API：`/api/index/{index|old}`、`/api/player/<bangumi_id>`、`/api/calendar`
- Kazumi 浏览 API：`/api/kazumi/timeline`、`/api/kazumi/recommend`、`/api/kazumi/recommend/all`、`/api/kazumi/subject/<id>`
- 番剧详情 API：`/api/kazumi/lookup`、`/api/kazumi/subject/<id>/{characters|staff|relations|comments}`
- 收藏状态 API：`GET /api/admin/bangumi/collection/<id>`、`POST /api/admin/bangumi/collection`、`POST /api/admin/bangumi/link`
- 缓存导入 / 导出 API：`GET /api/admin/cache/export`、`POST /api/admin/cache/import`（别名：`/api/admin/bangumi/cache/{export|import}`、`/api/admin/kazumi/cache/{export|import}`）
- 下载库导入 / 导出 API：`GET /api/admin/kazumi/downloads/export`、`POST /api/admin/kazumi/downloads/import`（ZIP：真实视频文件 + 已缓存封面 + 番剧信息 + 下载记录）
- 追番备份 API：`GET /api/admin/bangumi/export/bundle`、`POST /api/admin/bangumi/import/bundle`（ZIP：追番列表 + 已缓存封面 + 番剧文字信息）
- Kazumi 浏览缓存 API：`GET /api/admin/kazumi/browse/export`、`POST /api/admin/kazumi/browse/import`（ZIP：时间表 / 推荐缓存 + 已缓存封面 + 番剧文字信息）
- 管理 API：`/api/admin/*`，需要 `Authorization: Bearer <admin_token>`
- 自升级 API：`GET /api/admin/upgrade`（检查版本、源码目录）、`POST /api/admin/upgrade`（`go install`，body 可选 `{"mode":"remote|local","version":"v1.2.3"}`）、`POST /api/admin/upgrade/source`（同步源码，body 可选 `{"version":"v1.2.3"}`）
- 本地播放器 API：`POST /api/admin/play-local`（body `{"path":"/bangumi/..."}` 或 `{"path":"/kazumi-files/..."}`）
- 服务开关 API：`GET/PATCH /api/admin/http/settings`（`mcp_enabled`，修改后立即生效并写入配置）
- 日历订阅：`/resource/calendar.ics`
- MCP：`POST /mcp`（Streamable HTTP）、`GET /mcp/sse`、`POST /mcp/messages`
- 内置 Web UI：`/`（随二进制发布，无需安装）
- 媒体文件：`/bangumi/` 提供 `save_path`，`/kazumi-files/` 提供 Kazumi 下载目录，`/covers/` 提供封面缓存
- Web 前端静态资源启用 gzip 压缩与长期缓存（HTML 入口保持 no-cache），API 响应不缓存以保证数据实时性

管理 API 和 MCP 使用配置中的 `[http] admin_token`：

```bash
uani config get http admin_token
```

启动 HTTP 服务后打开 `http://127.0.0.1:8888/`，在侧边栏「设置」或令牌入口填入该 token 即可管理。

如需使用旧版外部前端，可设置 `http.serve_static_files = true` 并确保 `front_static_path/index.html` 存在。

MCP 客户端示例（Codex / Streamable HTTP）：

```toml
[mcp_servers.uani]
url = "http://127.0.0.1:8888/mcp"
headers = { Authorization = "Bearer <admin_token>" }
```

SSE 客户端使用 `http://127.0.0.1:8888/mcp/sse`。可用 MCP tools：上游兼容的 `cal`、`list`、`add`、`delete`、`search`、`update`、`seen`、`seen_forget`、`seen_mark`、`download`、`download_status`、`get_filter`、`set_filter`、`postprocess`、`set_status`，Kazumi 的 `kazumi_catalog`、`kazumi_list`、`kazumi_install`、`kazumi_update`、`kazumi_remove`、`kazumi_get_network`、`kazumi_set_network`、`kazumi_search`、`kazumi_chapters`、`kazumi_download`、`kazumi_download_status`、`kazumi_files`，以及 Bangumi 追番同步的 `bangumi_sync_status`、`bangumi_sync`、`bangumi_set_token`、`bangumi_configure_sync`、`bangumi_logout`。

## 配置

配置使用 TOML，兼容上游 BGmi 5.x 字段，例如：

```toml
data_source = "mikan_project"
download_delegate = "aria2-rpc"
save_path = "/path/to/bangumi"
db_path = "/path/to/bangumi.db"
max_path = 3
enable_path_formatter = false
path_formatter = "{name}/S{season:02d}/S{season:02d}E{episode:02d}.{suffix}"

[http]
admin_token = "<random token>"
serve_static_files = false
disable_web = false
disable_mcp = false
danmaku_api_url = ""

[player]
command = ""                         # 例如 "mpv --fullscreen {path}"；留空自动探测

[kazumi]
rule_repository = ""                 # 空值表示官方 KazumiRules 仓库
bangumi_acceleration = "mirror"      # direct / ech / mirror
bangumi_mirror = "https://next.bangumi.lat"   # 当前使用的 Bangumi 镜像（p1 默认）
bangumi_mirrors = ["https://next.bangumi.lat", "https://api.kazumi.fyi", "https://api.bgmapi.com"]
rules_mirror = "ghProxy"             # ghProxy / ghfast / gitcode / direct
image_acceleration = "ech"           # direct / ech / mirror

[bangumi]
access_token = ""                    # Bangumi 个人令牌，uani sync login 会写入
username = ""
sync_types = ["wish", "watched", "watching", "on_hold", "dropped"]
sync_types_version = 2
sync_enabled = false                 # 为 true 时 uani update 前自动同步
last_sync = 0

[webdav]
url = ""
username = ""
password = ""
enabled = false
sync_collectibles = true
last_sync = 0
```

环境变量使用 `UANI_` 前缀（如 `UANI_SAVE_PATH`、`UANI_DB_PATH`、`UANI_DOWNLOAD_DELEGATE`、`UANI_DATA_SOURCE`、`UANI_BANGUMI_ACCESS_TOKEN` 等）。为便于从 BGmi 迁移，旧的 `BGMI_` 前缀仍会被识别。

## 数据库迁移

首次运行或执行 `uani install` / `uani upgrade` 时，会自动识别 v4 数据库并迁移到 v5：

- `bangumi`：`keyword` → `id`，`update_time` → `update_day`
- `followed`：单集 `episode` → 剧集集合 `episodes`，并合并旧 `filter` 表
- `download`：`name` → `bangumi_name`，新增 `task_id`

迁移后旧 `cover` 目录会重命名为 `.cover`。

## 测试

```bash
go test ./...
go vet ./...
```

测试覆盖配置自动初始化、剧集/季度解析、数据源 HTML/XML 解析、控制器、数据库迁移、路径格式化、Kazumi 规则仓库/加速、Kazumi XPath/API 规则解析、Bangumi 剧情详情/角色/制作人员/关联缓存、缓存导入/导出、追番备份、下载库导入/导出、Kazumi 媒体文件元数据、收藏状态切换、内置 Web UI、Bangumi 追番同步，以及可选的 Python 脚本兼容（无 Python 时自动跳过）。

## 与上游的说明

- 上游的 FastAPI Web 服务与 MCP 入口已由 `uani http` 以原生 Go 实现，接口路径和行为尽量保持兼容。
- `uani install` 会安装 crontab，与上游一致；如不希望修改 crontab，可使用测试环境或手动执行其他初始化步骤。
- Python 脚本导入路径仍为 `bgmi.*`，以保持脚本兼容。
- CLI 提示信息保持上游英文风格。
