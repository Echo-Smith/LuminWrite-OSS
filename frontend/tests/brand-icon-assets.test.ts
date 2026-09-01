import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import test from "node:test";

const read = (path: string, encoding: BufferEncoding | null = "utf8") =>
  readFileSync(new URL(`../${path}`, import.meta.url), encoding ?? undefined);

function readPngHeader(path: string) {
  const png = read(path, null) as Buffer;
  assert.equal(png.subarray(1, 4).toString("ascii"), "PNG");
  return {
    width: png.readUInt32BE(16),
    height: png.readUInt32BE(20),
    colorType: png[25],
  };
}

test("brand SVGs preserve the V2 12/64 corner-radius ratio", () => {
  for (const path of ["public/favicon.svg", "public/favicon-dark.svg"]) {
    const svg = read(path) as string;
    assert.match(svg, /<clipPath id="appIconMask">/);
    assert.match(svg, /width="1024" height="1024" rx="192" ry="192"/);
  }
});

test("web and iOS PNGs expose RGBA assets at their target sizes", () => {
  for (const path of ["public/app-icon.png", "public/app-icon-dark.png"]) {
    assert.deepEqual(readPngHeader(path), { width: 1024, height: 1024, colorType: 6 });
  }
  for (const path of ["public/apple-touch-icon.png", "public/apple-touch-icon-dark.png"]) {
    assert.deepEqual(readPngHeader(path), { width: 180, height: 180, colorType: 6 });
  }
});

test("favicon, touch icon, inline React icon, and standalone pages share the theme mapping", () => {
  const index = read("index.html") as string;
  const component = read("src/components/brand-icon.tsx") as string;
  const themeHook = read("src/hooks/use-theme.ts") as string;
  const notFound = read("public/404.html") as string;
  const maintenance = read("public/maintenance.html") as string;

  assert.match(index, /id="app-favicon-svg"[^>]+href="\/favicon\.svg"/);
  assert.match(index, /id="apple-touch-icon"[^>]+href="\/apple-touch-icon\.png"/);
  assert.match(themeHook, /dark:[\s\S]+svg: "\/favicon-dark\.svg"/);
  assert.match(component, /src="\/favicon\.svg"[\s\S]+dark:hidden/);
  assert.match(component, /src="\/favicon-dark\.svg"[\s\S]+dark:block/);
  assert.match(notFound, /src="\/favicon-dark\.svg"/);
  assert.match(maintenance, /src="\/favicon-dark\.svg"/);
});
