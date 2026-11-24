function initFaqSearch(input) {
  var url = input.getAttribute("data-faq-search-url");
  var results = document.getElementById(input.getAttribute("data-faq-results"));
  var all = document.getElementById(input.getAttribute("data-faq-all"));
  if (!results) {
    return;
  }
  var itemClass = input.getAttribute("data-faq-item-class") || "";
  var answerClass = input.getAttribute("data-faq-answer-class") || "";
  var noteClass = input.getAttribute("data-faq-note-class") || "";
  var emptyText = input.getAttribute("data-faq-empty") || "";
  var errorText = input.getAttribute("data-faq-error") || "";
  var timer = null;
  var seq = 0;

  function clear() {
    while (results.firstChild) {
      results.removeChild(results.firstChild);
    }
  }

  function note(text) {
    var p = document.createElement("p");
    p.className = noteClass;
    p.textContent = text;
    results.appendChild(p);
  }

  function show(list) {
    clear();
    if (!list.length) {
      note(emptyText);
    }
    list.forEach(function (entry) {
      var details = document.createElement("details");
      details.className = itemClass;
      var summary = document.createElement("summary");
      var strong = document.createElement("strong");
      strong.textContent = entry.question;
      summary.appendChild(strong);
      var answer = document.createElement("p");
      answer.className = answerClass;
      answer.textContent = entry.answer;
      details.appendChild(summary);
      details.appendChild(answer);
      results.appendChild(details);
    });
    results.hidden = false;
    if (all) {
      all.hidden = true;
    }
  }

  function showError() {
    clear();
    note(errorText);
    results.hidden = false;
    if (all) {
      all.hidden = true;
    }
  }

  function reset() {
    seq++;
    clear();
    results.hidden = true;
    if (all) {
      all.hidden = false;
    }
  }

  function search() {
    var q = input.value.trim();
    if (q === "") {
      reset();
      return;
    }
    var mine = ++seq;
    fetch(url + "?q=" + encodeURIComponent(q), {
      headers: { "Accept": "application/json" },
      credentials: "same-origin"
    })
      .then(function (res) {
        if (!res.ok) {
          throw new Error("faq search status " + res.status);
        }
        return res.json();
      })
      .then(function (list) {
        if (mine === seq) {
          show(list);
        }
      })
      .catch(function (err) {
        console.error(err);
        if (mine === seq) {
          showError();
        }
      });
  }

  input.addEventListener("input", function () {
    clearTimeout(timer);
    timer = setTimeout(search, 250);
  });
}

Array.prototype.forEach.call(document.querySelectorAll("[data-faq-search-url]"), initFaqSearch);
