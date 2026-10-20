# 发版：签名与公证

[Releases](https://github.com/echoVic/cleanzo/releases) 里的 dmg 由维护者签名，并经过苹果公证。下面的步骤需要维护者的 Developer ID 证书；没有证书时怎么构建，见 README 的[「构建打包」](../README.md#构建打包)。

`bun run build` 用 Developer ID 签名 .app 和 dmg（hardened runtime + 时间戳），把 dmg 提交苹果公证，再把公证票据装订到 dmg 和 .app 上，配置在 `mygo.config.ts` 的 `macos` 字段。构建机需要：

- 钥匙串里有 `Developer ID Application: Zijie Hu (8LH7PFQJ89)` 的证书和私钥（`security find-identity -v -p codesigning` 能列出来）
- notarytool 凭据 profile `clipnest-notary`（与 `mygo.config.ts` 一致；`xcrun notarytool store-credentials clipnest-notary --apple-id <Apple ID> --team-id 8LH7PFQJ89`，需要 App 专用密码）

公证通常要几分钟。

自动更新包用更新签名私钥签名。私钥不在仓库和 `mygo.config.ts` 里，发布时通过环境变量传入：

```sh
MYGO_UPDATER_PRIVATE_KEY="$(cat <私钥文件>)" bun run build
```

没有私钥时照常构建，只是不生成更新包（`update-darwin-arm64.json`、完整包和增量包），已安装的 Cleanzo 就收不到这一版。上传前先确认这些文件在 `build/darwin-arm64` 里。

检查产物：

```sh
spctl -a -vvv -t exec build/darwin-arm64/Cleanzo.app   # source=Notarized Developer ID
xcrun stapler validate "build/darwin-arm64/Cleanzo 0.1.3.dmg"
```
