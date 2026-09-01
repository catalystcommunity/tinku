// Two small things, both progressive: the page is complete without this file.
const header = document.querySelector("[data-header]");
const year = document.querySelector("[data-year]");

if (year) {
  year.textContent = new Date().getFullYear();
}

if (header) {
  // A hairline appears under the header once the page has moved, so the
  // sticky bar separates from the content it is floating over.
  const setHeaderState = () => {
    header.classList.toggle("is-scrolled", window.scrollY > 8);
  };

  setHeaderState();
  window.addEventListener("scroll", setHeaderState, { passive: true });
}
