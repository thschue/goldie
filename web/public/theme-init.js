// Pre-paint theme init: applies the ThemeToggle choice before the first paint,
// so a stored dark theme never flashes light. It is an external file because
// the CSP (script-src 'self') blocks inline scripts. "system" leaves data-theme
// unset, and goldie.css follows prefers-color-scheme. The key and values match
// ThemeToggle.vue.
(function () {
  try {
    var theme = window.localStorage.getItem('goldie-theme');
    if (theme === 'light' || theme === 'dark') {
      document.documentElement.setAttribute('data-theme', theme);
    }
  } catch (e) {
    // Storage is off: follow the system preference.
  }
})();
