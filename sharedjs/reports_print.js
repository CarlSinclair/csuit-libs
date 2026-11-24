(function () {
  "use strict";
  window.addEventListener("load", function () {
    setTimeout(function () {
      try {
        window.print();
      } catch (e) {}
    }, 200);
  });
})();
