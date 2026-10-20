import { afterEach, expect, test } from "bun:test";
import { baseName, dirName, isWindows, isWithin, pathSep, setPlatformForTest, shortHome, splitPath } from "../src/platform";
import { displayPath } from "../src/react/large/format";
import { pathHintKind } from "../src/react/list/type-hint";
import { under } from "../src/react/list/selection";

afterEach(() => setPlatformForTest(undefined));

test("macOS paths retain their spelling, case and home abbreviation", () => {
  setPlatformForTest("darwin");
  expect(isWindows()).toBe(false);
  expect(pathSep()).toBe("/");
  expect(shortHome("/Users/alice")).toBe("~");
  expect(shortHome("/Users/alice/Documents/x")).toBe("~/Documents/x");
  expect(displayPath("/Users/alice")).toBe("/Users/alice");
  expect(displayPath("/Users/alice/x")).toBe("~/x");
  expect(splitPath("/Users/alice/x/")).toEqual(["Users", "alice", "x"]);
  expect(baseName("/")).toBe("/");
  expect(dirName("/Users/alice/x")).toBe("/Users/alice");
  expect(isWithin("/Users/A/x", "/Users/a")).toBe(false);
  expect(isWithin("/Users/alice2", "/Users/alice")).toBe(false);
});

test("Windows paths abbreviate home and compare complete components without case", () => {
  setPlatformForTest("win32");
  expect(isWindows()).toBe(true);
  expect(pathSep()).toBe("\\");
  expect(shortHome("C:\\Users\\Alice")).toBe("~");
  expect(shortHome("c:\\users\\Alice\\Downloads\\x.zip")).toBe("~\\Downloads\\x.zip");
  expect(displayPath("C:\\Users\\Alice\\x")).toBe("~\\x");
  expect(splitPath("C:\\Users\\Alice\\")).toEqual(["C:", "Users", "Alice"]);
  expect(baseName("C:\\Users\\Alice\\x.exe")).toBe("x.exe");
  expect(dirName("C:\\Users\\Alice\\x.exe")).toBe("C:\\Users\\Alice");
  expect(dirName("C:\\x.exe")).toBe("C:\\");
  expect(isWithin("C:\\USERS\\Alice\\x", "c:\\users\\alice")).toBe(true);
  expect(under("C:\\USERS\\Alice\\x", "c:\\users\\alice")).toBe(true);
  expect(isWithin("C:\\Users\\Alice2", "C:\\Users\\Alice")).toBe(false);
  expect(isWithin("C:\\x", "c:\\")).toBe(true);
});

test("Windows file hints use the leaf name and cover installers, shortcuts and images", () => {
  setPlatformForTest("win32");
  for (const [ext, kind] of Object.entries({ exe: "app", msi: "installer", msix: "installer", lnk: "app", zip: "archive", "7z": "archive", iso: "installer", vhdx: "vm" })) {
    expect(pathHintKind(`C:\\has.dot\\file.${ext.toUpperCase()}`)).toBe(kind);
  }
});
