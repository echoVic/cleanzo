import { defineConfig } from "vite";
import type { Plugin } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

/** 开发服务器在 main.ts 之前加载假运行时；它只在 ?mock 且没有 window.mygo 时生效。 */
function mockRuntime(): Plugin {
  return {
    name: "cleanzo-mock-runtime",
    apply: "serve",
    transformIndexHtml: () => [{ tag: "script", attrs: { type: "module", src: "/src/dev/mock-mygo.ts" }, injectTo: "head-prepend" }],
  };
}

export default defineConfig({
  plugins: [react(), tailwindcss(), mockRuntime()],
  server: {
    // The app loads this address during `mygo dev` (devUrl in mygo.json).
    port: 5173,
    strictPort: true,
    // The development app and the packaged builds are not the page's.
    watch: { ignored: ["**/.mygo/**", "**/build/**"] },
  },
});
