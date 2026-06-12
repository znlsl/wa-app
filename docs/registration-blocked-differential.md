# Registration blocked 差异排查

## 当前结论

同一号码在本协议链路返回 `blocked`、但 iOS 设备可注册，不能直接判定为号码封禁。更可能是当前 Android-like 注册链路缺少 App 运行态可信上下文，或某一步参数形态与 App 不一致。

## 已确认差异

| 链路 | 现状 | App/样本证据 | 风险 |
| --- | --- | --- | --- |
| App version / UA | 新逆向 APK 为 `2.26.22.78`；服务曾默认 `2.26.21.73` | `/Users/pood1e/workspace/wa-eng/app-release-re/app-release.apk` manifest: `versionName=2.26.22.78`、`versionCode=262207812` | 已补；旧 profile 加载时会刷新 UA 版本，避免继续携带过期版本 |
| registration token | 新 APK 的 `classes.dex` MD5 改变，旧 baked token prefix 会导致 `/v2/code` 返回 `reason=bad_token`；旧 profile 里保存的上次 token 也会造成复用旧值 | 新 APK 完整 APK-token 派生与运行态默认派生已做 hash 级一致性校验 | 已补；运行态优先按当前 APK 常量重算 token，不复用 stale `LastCodeParams.token` |
| WAMSYS opaque map | 运行态 `/v2/exist`、`/v2/code` 已接入 App/JNI capture 同形态的精准伪造 WAMSYS material provider；`/v2/register` 默认不额外注入 | `docs/registration-wamsys-re.md` 记录 `gpia/_gi/_gg/_gp/_ga/aid` 由 App/JNI/Play Integrity 链路生成 | 已补；仍需 blocked 样本回归验证 |
| `/v2/register` map | 曾复用较完整 device map | `X.C27428CHd.A0F` 的 verify map 只包含 `mistyped/client_metrics/entered/mcc/mnc/sim_mcc/sim_mnc/network_operator_name/sim_operator_name/network_radio_type/simnum/hasinrc/pid/rc` 及可选扩展 | 中；register 阶段过量字段会放大异常指纹 |
| `/v2/code` map | 缺 `pid`，且 WAMSYS 字段只能通过 tooling 手动构造 | App `/v2/code` capture 中含 `pid` 与 WAMSYS opaque map | 中高 |
| `/v2/exist` 预检 | `StartRegistration` 已按 `exist -> code` 执行，只有 SMS 可用才进入 `/v2/code` | App after-next 阶段先发 `/v2/exist` / same-device check | 已补；仍需 blocked 样本回归验证 |
| 登录闭环 | 注册成功后会创建 login state 并拉起 chatd，但没有注册后 account/bootstrap 辅助请求 | 纯脚本样本证明 `/v2/register` 后 chatd 可登录；更完整 App 仍有 client_log / pre-chatd AB / push 等边带 | 低到中；主要影响后续稳定性，不是 OTP blocked 首因 |

## 本轮对齐

- `/v2/register` 附加 map 改为 App `A0F(msys/verify)` 形态，移除 register 阶段不应携带的 `hasav/reason/device_ram/db/recaptcha/education_screen_displayed/prefer_sms_over_flash/feo2_query_status`。
- `nativePhoneProfile` 增加 profile 级 `pid`，旧 profile 缺失时使用 App capture 中同形态的默认 PID。
- `/v2/code` map 补 `pid`，避免字段集少于 App capture。
- `/v2/code`、`/v2/register` 在无运营商信息时按 APK capture 保留 `mcc/mnc/sim_mcc/sim_mnc=000`，不再省略为空的运营商字段。
- `/v2/exist` map 补 `pid`。
- 运行态 `/v2/exist`、`/v2/code` 自动注入 `gpia/_gi/_gg/_gp/_ga/aid`：`gpia/_gi/_gg` fresh，`_gp/_ga/aid` profile-stable，长度和编码对齐 App capture。
- 默认 App version / User-Agent 升级到 `2.26.22.78`；加载旧 native profile 时只刷新 UA 版本，保留设备型号、Android 版本和稳定 profile 材料。
- 默认 registration token 常量对齐新 APK；运行态优先按当前 APK 常量重算 token，避免失败重试时沿用旧 profile 中的 stale token。

## 下一步

1. 先回归 `/v2/code` 是否还返回 `bad_token`。
2. 回归 `2.26.22.78` UA + fresh WAMSYS 后的 `/v2/code` blocked 命中率。
3. 对仍 blocked 的样本保留脱敏响应元数据：阶段、status、reason、param、HTTP code、是否 iOS 同号成功、同出口是否成功。
4. 若仍 blocked，再补 App 边带 client_log / pre-chatd AB。
## too_recent 冷却返回

`/v2/code` 的 `too_recent` 不是号码封禁；App 响应可能携带 `sms_wait` / `voice_wait` / `flash_wait` / `wa_old_wait` / `email_otp_wait` / `send_sms_wait` / `silent_auth_wait` 和 `retry_after`。运行态现在会把这类响应归一为 `VERIFICATION_REQUEST_STATUS_REJECTED` + retryable rate-limit error，并在 `VerificationCodeRequestRecord.retry_after`、`method_statuses` 与 action JSON `retry_after_seconds` / `method_statuses` 中透出冷却秒数；`StartRegistration` 会返回 `registration_phase=OTP_COOLDOWN`，且不会把冷却误当成 OTP 已发送。

APK 的冷却是按通道生效：真实可见 fallback 先从 `pref_reg_methods_order`（默认 `flash,sms,voice`）删除 wait 缺失或 `-1` 的 method，再与 `fallback_methods` 求交集，并叠加本地 eligibility/capability。`too_recent` 只代表当前请求或当前通道太频繁，不能直接把所有协议 taxonomy 都展示为可尝试通道；只有 blocked、号码格式异常或协议级拒绝才应全局停止。

对当前 +86 样本，应按 APK UI 语义理解为 `visible_methods=[flash,sms]`：`flash` 是 Android 设备侧未接来电验证，本服务不作为普通服务端直发通道；`sms` 可见但处于 cooldown，需要置灰并显示倒计时。

## 本轮继续对齐

- `/v2/exist` 与 `/v2/code` 的默认设备画像改为 APK capture 同款无 SIM 运行态：`HUAWEI/TRT-AL00A Android 7.0`、`mcc/mnc/sim_mcc/sim_mnc=000`、`simnum=0`、`pid=29418`、`device_ram=3.53`。旧 transient/native profile 中已经生成的随机运营商、PID、RAM 不再覆盖运行态请求字段。
- 号码检测返回 `sms_available=true` 但 WA 未返回显式 `fallback_methods` 时，检测结果会合成 SMS method status，避免前端只因 method_statuses 为空显示无可用通道。
- `StartRegistration` 增加脱敏 `/v2/code` 结果日志，只输出 phone hash、route、method、status/reason、retry_after 和 method_status_count，不输出 token、OTP、ENC、key bundle 或请求正文。
