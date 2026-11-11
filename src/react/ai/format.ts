import { shortHome } from "../../platform";
/** 字节展示就是 shared.ts 那一份。 */
export { human } from "../../shared";

export function displayPath(p: string): string {
  return shortHome(p, true);
}
