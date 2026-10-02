import { mkdir, open, rename, lstat, readFile, unlink } from "node:fs/promises";
import { join } from "node:path";
import { randomBytes } from "node:crypto";
import {
  credential,
  requireValue,
  type LmmCredential,
} from "./vendor/protocol.ts";

export class Store {
  readonly directory: string;
  readonly issuer: string;
  constructor(directory: string, issuer: string) {
    this.directory = directory;
    this.issuer = issuer;
  }
  async read(): Promise<LmmCredential> {
    const directory = await lstat(this.directory);
    requireValue(
      directory.isDirectory() &&
        !directory.isSymbolicLink() &&
        (directory.mode & 0o077) === 0,
      "Credential directory must be private (0700).",
    );
    const path = join(this.directory, "oauth.json");
    const info = await lstat(path);
    requireValue(
      info.isFile() && !info.isSymbolicLink() && (info.mode & 0o077) === 0,
      "Credential file must be private (0600).",
    );
    return credential(JSON.parse(await readFile(path, "utf8")), this.issuer);
  }
  async write(value: LmmCredential) {
    credential(value, this.issuer);
    await mkdir(this.directory, { recursive: true, mode: 0o700 });
    const directory = await lstat(this.directory);
    requireValue(
      directory.isDirectory() &&
        !directory.isSymbolicLink() &&
        (directory.mode & 0o077) === 0,
      "Credential directory must be private (0700).",
    );
    const temporary = join(
      this.directory,
      `.oauth-${randomBytes(12).toString("hex")}`,
    );
    const handle = await open(temporary, "wx", 0o600);
    try {
      await handle.writeFile(JSON.stringify(value));
      await handle.sync();
    } finally {
      await handle.close();
    }
    try {
      await rename(temporary, join(this.directory, "oauth.json"));
      const dir = await open(this.directory, "r");
      try {
        await dir.sync();
      } finally {
        await dir.close();
      }
    } finally {
      await unlink(temporary).catch(() => {});
    }
  }
  async remove() {
    await unlink(join(this.directory, "oauth.json")).catch((error) => {
      if (error.code !== "ENOENT") throw error;
    });
  }
}
