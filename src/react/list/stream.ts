/**
 * 在迭代通道之前先接住完成 Promise：通道还开着时桥接就失败了，也不会成为未处理的
 * rejection。迭代结束后调用返回的函数，后端出错时在这里抛出。
 */
export function settled(done: Promise<void>): () => Promise<void> {
  const outcome = done.then(() => null, (error: unknown) => ({ error }));
  return async () => {
    const failed = await outcome;
    if (failed) throw failed.error;
  };
}
