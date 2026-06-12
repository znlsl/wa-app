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
- `/v2/code`、`/v2/register` 继续沿用 profile 生成出的运营商上下文；无运营商信息时按 APK `C253119h.A00(null)` / `A0H` 行为发送 `mcc/mnc/sim_mcc/sim_mnc=000`，不省略四个运营商字段。
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

- 回滚固定整机画像但保留 APK 无运营商默认语义：最新 App 注册需要保持 profile 级设备/PID/RAM 自洽；运营商缺失时 `mcc/mnc/sim_mcc/sim_mnc` 仍按 APK 发送 `000`。
- 号码检测返回 `sms_available=true` 但 WA 未返回显式 `fallback_methods` 时，检测结果会合成 SMS method status，避免前端只因 method_statuses 为空显示无可用通道。
- `StartRegistration` 增加脱敏 `/v2/code` 结果日志，只输出 phone hash、route、method、status/reason、retry_after 和 method_status_count，不输出 token、OTP、ENC、key bundle 或请求正文。
- `/v2/code`/`/v2/exist` HTTP envelope 保留最新 APK `RetryingHttpClient` 的 `H` 表单键；当前服务端运行态没有 AndroidKeyStore attestation，按 APK attestation 不可用路径发送 `ENC=<cipher>&H=`，不再伪造自签名 `Authorization` 证书链；运行日志仍不输出 ENC/H/Authorization。
- `/v2/code` 补最新 APK `KotlinRegistrationBridge.A06 -> A0P` 的 `advertising_id` 标量；非 EU profile 生成稳定 UUID，避免和真实 App 可用 GAID 的请求形态继续偏离。
- `/v2/exist`/`/v2/code` 的 `feo2_query_status` 默认值对齐最新 APK shared-pref 读取默认值 `did_not_query`；加载旧 profile 时把旧的 `error_security_exception` 视为 stale 运行态结果并刷新为默认值。
- `/v2/code`/`/v2/exist` HTTP transport 对齐最新 APK 静态形态：不再由 Go transport 显式发送 `Connection: close`，也不手动补 `Connection: Keep-Alive`；保留 `WaMsysRequest` / `request_token` 的 APK 头名形态。
- `/v2/code`/`/v2/exist` 的 `db` 不再固定成 hook/emulator capture 里的 `1`；最新 APK 该字段来自 `Settings.Global["adb_enabled"]`，默认指纹按普通手机发送 `0`。`+84` 号码的 transient profile 增加 VN 运营商 MCC/MNC 候选，避免继续落到无 SIM `000/000` 形态。
- 回滚运行态 Pure-Go WAMSYS fake fallback：`gpia/_gi/_gg/_gp/_ga/aid` 是 APK/JNI/Play Integrity 可信材料，当前服务没有真实 Android oracle 时不再自动伪造并发送，避免 `/v2/code` 因可区分假材料继续落到 `no_routes`。
- 最新 APK 对 `/v2/exist` / same-device check 的 `no_routes` 仍会继续解析 wait 与 fallback 元数据；wa-app 只把 blocked、号码异常、协议错误和冷却作为预检终局。SMS 直发是否真正可用由后续 `/v2/code` 决定，避免在预检阶段误报“暂无可用验证通道”。
- 显式选择 SMS fallback 时，APK 会把 `pref_prefer_sms_over_flash=true` 写入请求 map；wa-app 对直发 SMS 同步发送 `prefer_sms_over_flash=true`，避免服务端继续按 flash 优先分流。
