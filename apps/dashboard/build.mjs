import { build } from "esbuild";
import { readFileSync } from "node:fs";

const { version } = JSON.parse(readFileSync("package.json", "utf8"));
await build({
  entryPoints: ["src/index.ts"],
  bundle: true,
  format: "esm",
  target: "es2022",
  minify: true,
  sourcemap: false,
  legalComments: "eof",
  outfile: "dist/deckbeacon-cards.js",
  define: { __VERSION__: JSON.stringify(version) },
});
console.log(`built dist/deckbeacon-cards.js v${version}`);
