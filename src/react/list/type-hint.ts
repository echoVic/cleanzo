// Name-based hints only: never used to decide whether a path may be removed.
export type PathHintKind = "app" | "installer" | "archive" | "library" | "vm" | "cache" | "temp" | "log" | "config" | "database" | "source" | "media" | "document" | "folder" | "unknown";

export function pathHintKind(path: string, isDir = false): PathHintKind {
  const name = path.split("/").filter(Boolean).at(-1)?.toLowerCase() ?? "";
  const ext = name.match(/\.[^.]+$/)?.[0] ?? "";
  if (ext === ".app") return "app";
  if ([".dmg", ".pkg", ".mpkg", ".xip"].includes(ext)) return "installer";
  if ([".photoslibrary", ".photolibrary", ".musiclibrary", ".imovielibrary", ".fcpbundle"].includes(ext)) return "library";
  if ([".pvm", ".vmwarevm", ".utm", ".vmdk", ".qcow2", ".sparsebundle"].includes(ext)) return "vm";
  if (isDir && ["cache", ".cache", "caches", "code cache", "gpucache", "cacheddata", "node_modules"].includes(name)) return "cache";
  if (["tmp", "temp", "temporaryitems"].includes(name) || [".tmp", ".temp", ".part", ".crdownload", ".download"].includes(ext)) return "temp";
  if (ext === ".log" || (isDir && name === "logs")) return "log";
  if ([".zip", ".gz", ".tgz", ".bz2", ".xz", ".7z", ".rar", ".tar"].includes(ext)) return "archive";
  if ([".plist", ".ini", ".conf", ".yaml", ".yml", ".json", ".toml"].includes(ext) || (isDir && name === "preferences")) return "config";
  if ([".db", ".sqlite", ".sqlite3", ".db-wal", ".db-shm"].includes(ext)) return "database";
  if ([".go", ".ts", ".tsx", ".js", ".py", ".swift", ".rs", ".c", ".h", ".xcodeproj", ".xcworkspace"].includes(ext)) return "source";
  if ([".jpg", ".jpeg", ".png", ".heic", ".gif", ".mp4", ".mov", ".mp3", ".wav"].includes(ext)) return "media";
  if ([".pdf", ".doc", ".docx", ".xls", ".xlsx", ".ppt", ".pptx", ".pages", ".numbers", ".key", ".txt", ".md"].includes(ext)) return "document";
  return isDir ? "folder" : "unknown";
}
