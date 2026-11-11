/**
 * The base class of every tinku element. It is a Zest BaseComponent in the
 * light DOM, so the one stylesheet applies.
 *
 * - `build()` makes the elements one time, at the first connect. Make every
 *   form control here, so that a render never replaces a field that a
 *   person is typing in.
 * - `render()` changes the parts that changed. It runs after each connect,
 *   after each change of a watched store collection, and after
 *   `invalidate()`.
 * - `connected()` adds the subscriptions of one connect (`own`, `listen`).
 *
 * Do not give a subclass a member named after an `HTMLElement` member
 * (`title`, `remove`, `hidden`, ...).
 *
 * A throw from `build()` or `render()` does not blank the page: the element
 * sends `tinku-crash`, and the shell shows the crash panel in place of the
 * page. A reader can still use the navigation.
 */
import { BaseComponent } from "@catalystcommunity/base-components";
import { store } from "~/state/store";

export const CRASH_EVENT = "tinku-crash";

export class TinkuElement extends BaseComponent {
  /** Store collections whose changes render this element again. */
  static watches: readonly string[] = [];
  #built = false;

  connectedCallback(): void {
    super.connectedCallback();
    this.guard(() => {
      if (!this.#built) {
        this.#built = true;
        this.build();
      }
    });
    for (const collection of (this.constructor as typeof TinkuElement).watches) {
      this.own(store.subscribe(() => this.invalidate(), { collection, isActive: () => this.isActive() }));
    }
    this.connected();
    this.render();
  }

  /** Make the elements. Runs one time. */
  protected build(): void {}

  /** Add listeners and subscriptions. Runs at each connect. */
  protected connected(): void {}

  /**
   * Subclasses implement `update()`. `render()` is the BaseComponent entry
   * point, which `invalidate()` calls; it wraps `update()` so that a throw
   * becomes a crash event, not a blank page.
   */
  render(): void {
    if (!this.#built) return;
    this.guard(() => this.update());
  }

  /** Change the parts that changed. */
  protected update(): void {}

  /** Run `fn`. A throw becomes a `tinku-crash` event from this element. */
  protected guard(fn: () => void): void {
    try {
      fn();
    } catch (error) {
      this.crash(error);
    }
  }

  /** Tell the shell that this element cannot show itself. */
  crash(error: unknown): void {
    console.error(error);
    this.dispatchEvent(new CustomEvent(CRASH_EVENT, { bubbles: true, detail: error }));
  }
}

/** Define a tag one time. */
export function define(tag: string, cls: CustomElementConstructor): void {
  if (!customElements.get(tag)) customElements.define(tag, cls);
}

/**
 * The state of one asynchronous load, which an element reads in
 * `update()`. It replaces Solid's `createResource`.
 *
 * - `load()` starts the fetch again. A response that is older than the
 *   newest request is dropped, so a slow first answer cannot overwrite a
 *   fast second one.
 * - When a load ends, the owner renders again, but only while it is on the
 *   page.
 * - `value` stays as it was during a reload, so a list does not flash empty.
 */
export class Resource<T> {
  value: T | undefined = undefined;
  error: unknown = undefined;
  loading = false;
  #seq = 0;

  constructor(
    private owner: TinkuElement,
    private fetcher: () => Promise<T>,
  ) {}

  /** Start a load. The promise settles when it ends, and never rejects. */
  async load(): Promise<void> {
    const seq = ++this.#seq;
    this.loading = true;
    this.owner.invalidate();
    try {
      const value = await this.fetcher();
      if (seq !== this.#seq) return;
      this.value = value;
      this.error = undefined;
    } catch (error) {
      if (seq !== this.#seq) return;
      this.error = error;
    } finally {
      if (seq === this.#seq) {
        this.loading = false;
        if (this.owner.isActive()) this.owner.invalidate();
      }
    }
  }
}
