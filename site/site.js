// The one thing that needs JavaScript: the Copy button. Without it the button stays hidden.
(function () {
  var b = document.getElementById('copy'), c = document.getElementById('cmd');
  if (!b || !c || !navigator.clipboard) return;
  document.documentElement.className = 'js';
  b.addEventListener('click', function () {
    navigator.clipboard.writeText(c.textContent.trim()).then(function () {
      b.textContent = 'Copied!';
      setTimeout(function () { b.textContent = 'Copy'; }, 1500);
    });
  });
})();
