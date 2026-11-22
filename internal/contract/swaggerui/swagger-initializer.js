// Starts Swagger UI on the spec this server serves, and on nothing else.
// queryConfigEnabled is off so a query string cannot point the page at
// another spec or configuration, and the standalone layout (which has a box
// to load any URL) is not used.
window.onload = function () {
  window.ui = SwaggerUIBundle({
    url: "/openapi.json",
    dom_id: "#swagger-ui",
    queryConfigEnabled: false,
    deepLinking: false,
    presets: [SwaggerUIBundle.presets.apis],
    layout: "BaseLayout",
  });
};
