import { defineConfig } from "mygo-cli";

// The one place the version lives: the app reads it at run time, and the
// disk image's volume carries it, so mounted versions are told apart.
const version = "0.2.2";

export default defineConfig({
  name: "Cleanzo",
  identifier: "com.cleanzo.app",
  version,
  // `mygo dev` runs devCommand and loads devUrl; `mygo build` runs
  // buildCommand and embeds frontendDist into the app.
  devUrl: "http://localhost:5173",
  devCommand: "bun run dev:web",
  buildCommand: "bun run build:web",
  frontendDist: "dist",
  bindings: "src/mygo.ts",
  out: "build",
  // Signed auto-updates from this repository's GitHub releases. The secret
  // key stays outside the repository: release builds read it from
  // MYGO_UPDATER_PRIVATE_KEY (docs/RELEASING.md).
  updates: {
    publicKey: "0ZE+ktIAa8WWCN9qm3Gyg+4q1He4iPKclVBRfi7lEQc=",
    github: "echoVic/cleanzo",
  },
  // `mygo build` signs the app and the dmg with this Developer ID (hardened
  // runtime + timestamp), notarizes the dmg and staples both; `mygo dev`
  // stays ad hoc. The profile is from `xcrun notarytool store-credentials`.
  macos: {
    dmgTitle: `Cleanzo ${version}`,
    signingIdentity: "Developer ID Application: Zijie Hu (8LH7PFQJ89)",
    notarize: { keychainProfile: "clipnest-notary" },
  },
  // Windows 签名：certificate（密码 MYGO_WINDOWS_CERTIFICATE_PASSWORD）或 signCommand。
  windows: {},
});
