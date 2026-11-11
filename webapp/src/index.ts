// The Zest tokens first: index.css sets them from the tinku palette, and
// an unlayered rule wins over the `zest` layer.
import "@catalystcommunity/design-tokens/src/tokens.css";
import "./index.css";
import { defineZest } from "~/ui/zest";
import { routeLinks, startRouter } from "~/app/router";
import { useSession } from "~/lib/session";
import { I18N } from "~/i18n";
import { store } from "~/state/store";
import "./App";

const root = document.getElementById("root");
if (!root) {
  throw new Error("index.html is missing its #root element");
}

defineZest();
startRouter();
routeLinks();
void useSession().refresh();
root.append(document.createElement("tinku-app"));

// A new locale makes the whole shell again: every string is read when an
// element builds, so no element has to follow the locale itself.
store.subscribe(() => root.replaceChildren(document.createElement("tinku-app")), { collection: I18N });
