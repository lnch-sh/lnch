import { resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import tailwindcss from "@tailwindcss/vite";
import { cloudflare } from "@cloudflare/vite-plugin";

const root = fileURLToPath(new URL(".", import.meta.url));

export default defineConfig({
  plugins: [tailwindcss(), cloudflare({ viteEnvironment: { name: "worker" } })],
  environments: {
    client: {
      build: {
        rollupOptions: {
          input: {
            main: resolve(root, "index.html"),
            docs: resolve(root, "docs/index.html"),
          },
        },
      },
    },
    worker: { build: { sourcemap: true } },
  },
});
