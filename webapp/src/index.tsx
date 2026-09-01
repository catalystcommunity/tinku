/* @refresh reload */
import { render } from "solid-js/web";
import App from "./App";
import { I18nProvider } from "./i18n";
import { SessionProvider } from "./lib/session";
import "./index.css";

const root = document.getElementById("root");
if (!root) {
  throw new Error("index.html is missing its #root element");
}

// I18nProvider is outermost: it sets the document language and every other
// provider below it may need a translated string.
render(
  () => (
    <I18nProvider>
      <SessionProvider>
        <App />
      </SessionProvider>
    </I18nProvider>
  ),
  root,
);
