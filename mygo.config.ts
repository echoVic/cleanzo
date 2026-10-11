import { defineConfig } from "mygo-cli";

// The one place the version lives: the app reads it at run time, and the
// disk image's volume carries it, so mounted versions are told apart.
const version = "0.1.3";

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
  // `mygo build` signs the app and the dmg with this Developer ID (hardened
  // runtime + timestamp), notarizes the dmg and staples both; `mygo dev`
  // stays ad hoc. The profile is from `xcrun notarytool store-credentials`.
  macos: {
    dmgTitle: `Cleanzo ${version}`,
    signingIdentity: "Developer ID Application: Zijie Hu (8LH7PFQJ89)",
    notarize: { keychainProfile: "notary" },
  },
});
