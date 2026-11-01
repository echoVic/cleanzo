import { Fragment } from "react";

/** 优先在斜杠处折行；超长单段（如 bundle id）交给 overflow-wrap 兜底，避免把文件名拆成半截。 */
export function PathText({ path }: { path: string }) {
  const [first, ...rest] = path.split("/");
  return (
    <>
      {first}
      {rest.map((seg, i) => (
        <Fragment key={i}>/<wbr />{seg}</Fragment>
      ))}
    </>
  );
}
