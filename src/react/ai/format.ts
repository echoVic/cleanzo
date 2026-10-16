/** 与 shared.ts 相同的字节展示。 */
export function human(n: number): string {
  if (n < 1024) return `${n} B`;
  let v = n / 1024;
  const units = ["KB", "MB", "GB", "TB"];
  let i = 0;
  while (v >= 1024 && i < units.length - 1) { v /= 1024; i++; }
  return `${v.toFixed(v >= 100 || i === 0 ? 0 : 1)} ${units[i]}`;
}

export function displayPath(p: string): string {
  return p.replace(/^\/Users\/[^/]+\//, "~/");
}
