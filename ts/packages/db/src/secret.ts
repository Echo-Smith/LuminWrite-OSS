import { inspect } from "node:util";

/** 解密后的密钥。日志、JSON 和 inspect 都看不到原文，只有 reveal() 拿得到。 */
export class Secret {
  readonly #value: string;

  constructor(value: string) {
    this.#value = value;
  }

  reveal(): string {
    return this.#value;
  }

  toString(): string {
    return "[redacted]";
  }

  toJSON(): string {
    return "[redacted]";
  }

  [inspect.custom](): string {
    return "[redacted]";
  }
}
