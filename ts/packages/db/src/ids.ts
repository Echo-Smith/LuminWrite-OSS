import { customAlphabet } from "nanoid";

const alphabet = customAlphabet("0123456789abcdefghijklmnopqrstuvwxyz", 16);

export function newId(prefix: string): string {
  return `${prefix}_${alphabet()}`;
}
