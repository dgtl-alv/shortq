"use strict";

window.addEventListener("load", function () {
  window.ui = SwaggerUIBundle({
    url: "/docs/openapi.yaml",
    dom_id: "#swagger-ui",
    deepLinking: true,
    persistAuthorization: false,
    displayRequestDuration: true,
    tryItOutEnabled: true,
  });
});
