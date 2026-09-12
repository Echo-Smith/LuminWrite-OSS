// 注册 @ 别名解析钩子，供 node --test 直载使用 "@/..." 的前端源码
import { register } from "node:module";
register(new URL("./alias-resolver.mjs", import.meta.url));
