// A DOM for the `node --test` frontend tests. jsdom cannot be bundled into a
// single self-contained file (it does dynamic require() of Node builtins and
// reads data files such as its default stylesheet from its own package dir at
// runtime), so this is a thin re-export resolved against jsdom's vendored
// install tree. That tree is committed as the single deterministic
// jsdom-node_modules.tar.gz in this directory; unpack.sh extracts it to
// ./node_modules before the tests run. See rebuild.sh.
export { JSDOM } from "jsdom";
