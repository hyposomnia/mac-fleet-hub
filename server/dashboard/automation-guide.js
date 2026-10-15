const apiOrigin = window.location.origin;

document.querySelectorAll('[data-api-path]').forEach((node) => {
  node.textContent = `${apiOrigin}${node.dataset.apiPath}`;
});
