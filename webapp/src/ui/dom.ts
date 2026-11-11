/**
 * Plain DOM helpers. `h` makes one element; it keeps no state and binds
 * nothing. A component makes its elements once and changes them itself.
 */

export type Child = Node | string | number | null | undefined | false | Child[];

/**
 * Attributes of `h`:
 * - `onClick: fn` adds a listener ("click"). `onKeyDown` is "keydown".
 * - `.value: x` sets a property, not an attribute.
 * - `style: {color: "red", "--sw": c}` sets style properties.
 * - `true` sets an empty attribute; `false`, `null`, and `undefined` set nothing.
 */
export type Attrs = Record<string, unknown>;

const SVG_NS = "http://www.w3.org/2000/svg";
const SVG_TAGS = new Set(["svg", "path", "circle", "rect", "g", "line"]);

export function h<K extends keyof HTMLElementTagNameMap>(tag: K, attrs?: Attrs | null, ...children: Child[]): HTMLElementTagNameMap[K];
export function h(tag: string, attrs?: Attrs | null, ...children: Child[]): HTMLElement;
export function h(tag: string, attrs?: Attrs | null, ...children: Child[]): Element {
  const el = SVG_TAGS.has(tag) ? document.createElementNS(SVG_NS, tag) : document.createElement(tag);
  if (attrs) setAttrs(el, attrs);
  el.append(...nodes(children));
  return el;
}

export function setAttrs(el: Element, attrs: Attrs): void {
  for (const [name, value] of Object.entries(attrs)) {
    if (name.startsWith(".")) {
      (el as unknown as Record<string, unknown>)[name.slice(1)] = value;
    } else if (/^on[A-Z]/.test(name)) {
      if (typeof value === "function") el.addEventListener(name.slice(2).toLowerCase(), value as EventListener);
    } else if (name === "style" && value && typeof value === "object") {
      const style = (el as HTMLElement).style;
      for (const [k, v] of Object.entries(value)) {
        if (v === null || v === undefined || v === false) style.removeProperty(k);
        else style.setProperty(k.startsWith("--") ? k : k.replace(/[A-Z]/g, (c) => `-${c.toLowerCase()}`), String(v));
      }
    } else {
      setAttr(el, name === "className" ? "class" : name, value);
    }
  }
}

/** Set, clear (false, null, undefined), or set empty (true). */
export function setAttr(el: Element, name: string, value: unknown): void {
  if (value === false || value === null || value === undefined) el.removeAttribute(name);
  else el.setAttribute(name, value === true ? "" : String(value));
}

export function nodes(children: Child[]): Node[] {
  const out: Node[] = [];
  const add = (c: Child) => {
    if (Array.isArray(c)) c.forEach(add);
    else if (c === null || c === undefined || c === false) return;
    else out.push(c instanceof Node ? c : document.createTextNode(String(c)));
  };
  children.forEach(add);
  return out;
}

/** Replace the children of `el`. */
export function fill(el: Element, ...children: Child[]): void {
  el.replaceChildren(...nodes(children));
}

/** Show or hide an element. */
export function show(el: HTMLElement, visible: unknown): void {
  el.hidden = !visible;
}

/** Set text only when it changes, so a screen reader does not read it again. */
export function setText(el: Node, text: string): void {
  if (el.textContent !== text) el.textContent = text;
}

/**
 * Keep the children of `parent` in the order of `items`, one element for
 * each key. An element for a key that stays is moved, not made again, so
 * focus and input state stay. `update` runs for each item.
 */
export function syncKeyed<T, E extends Element = HTMLElement>(
  parent: Element,
  items: readonly T[],
  key: (item: T) => string,
  create: (item: T) => E,
  update?: (el: E, item: T, index: number) => void,
): void {
  const old = new Map<string, E>();
  for (const child of [...parent.children]) {
    const k = (child as HTMLElement).dataset?.key;
    if (k !== undefined) old.set(k, child as E);
  }
  let cursor: ChildNode | null = parent.firstChild;
  items.forEach((item, i) => {
    const k = key(item);
    let el = old.get(k);
    if (el) old.delete(k);
    else {
      el = create(item);
      (el as unknown as HTMLElement).dataset.key = k;
    }
    update?.(el, item, i);
    if (cursor === el) cursor = el.nextSibling;
    else parent.insertBefore(el, cursor);
  });
  for (const el of old.values()) el.remove();
}

let idCount = 0;
/** A unique element id. */
export function uid(prefix = "tk"): string {
  return `${prefix}-${++idCount}`;
}
