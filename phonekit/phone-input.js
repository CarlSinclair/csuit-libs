(function () {
  "use strict";

  var SCRIPT_SRC = (document.currentScript && document.currentScript.src) || "";
  var FLAG_BASE = "/static/vendor/flags/";
  try {
    if (SCRIPT_SRC) FLAG_BASE = new URL("/static/vendor/flags/", SCRIPT_SRC).href;
  } catch (err) {
    console.error("phone-input.js: cannot derive flag base from script URL", err);
  }

  try {
    var sheet = new CSSStyleSheet();
    sheet.replaceSync(`.phone-input-group{display:flex;gap:6px;align-items:stretch}
.phone-input-group .phone-plus-prefix{flex:0 0 auto;display:flex;align-items:center;padding:0 4px;font-weight:700;color:var(--text-muted,inherit)}
.phone-input-group .phone-country-code{flex:0 0 64px;min-width:0;text-align:center;padding-inline-start:6px;padding-inline-end:6px}
.phone-input-group .phone-national{flex:1 1 auto;min-width:0}
.phone-input-group .phone-flag{flex:0 0 auto;align-self:center;width:20px;height:15px;border-radius:2px;box-shadow:0 0 0 1px rgba(128,128,128,.35)}
.phone-input-group .phone-flag[hidden]{display:none}`);
    document.adoptedStyleSheets = (document.adoptedStyleSheets || []).concat([sheet]);
  } catch (err) {
    console.error("phone-input.js: constructable stylesheet unavailable", err);
  }

  if (window.__phoneKitLoaded) return;
  window.__phoneKitLoaded = true;

  // Used only for two things: (1) optional autocomplete suggestions while
  // typing the country code, and (2) figuring out where the country code
  // ends and the national number begins when re-loading an existing stored
  // "+<digits>" value. The country-code field itself is always a plain free
  // text input, never a forced dropdown - typing any digits is valid.
  var CODES = [
    ["93", "Afghanistan"], ["355", "Albania"], ["213", "Algeria"], ["1684", "American Samoa"],
    ["376", "Andorra"], ["244", "Angola"], ["1264", "Anguilla"], ["1268", "Antigua and Barbuda"],
    ["54", "Argentina"], ["374", "Armenia"], ["297", "Aruba"], ["61", "Australia"], ["43", "Austria"],
    ["994", "Azerbaijan"], ["1242", "Bahamas"], ["973", "Bahrain"], ["880", "Bangladesh"],
    ["1246", "Barbados"], ["375", "Belarus"], ["32", "Belgium"], ["501", "Belize"], ["229", "Benin"],
    ["1441", "Bermuda"], ["975", "Bhutan"], ["591", "Bolivia"], ["387", "Bosnia and Herzegovina"],
    ["267", "Botswana"], ["55", "Brazil"], ["246", "British Indian Ocean Territory"],
    ["1284", "British Virgin Islands"], ["673", "Brunei"], ["359", "Bulgaria"], ["226", "Burkina Faso"],
    ["257", "Burundi"], ["855", "Cambodia"], ["237", "Cameroon"], ["1", "Canada / United States"],
    ["238", "Cabo Verde"], ["1345", "Cayman Islands"], ["236", "Central African Republic"], ["235", "Chad"],
    ["56", "Chile"], ["86", "China"], ["57", "Colombia"], ["269", "Comoros"], ["243", "Congo (DRC)"],
    ["242", "Congo (Republic)"], ["682", "Cook Islands"], ["506", "Costa Rica"], ["385", "Croatia"],
    ["53", "Cuba"], ["599", "Curaçao"], ["357", "Cyprus"], ["420", "Czechia"], ["45", "Denmark"],
    ["253", "Djibouti"], ["1767", "Dominica"], ["1809", "Dominican Republic"], ["670", "East Timor"],
    ["593", "Ecuador"], ["20", "Egypt"], ["503", "El Salvador"], ["240", "Equatorial Guinea"],
    ["291", "Eritrea"], ["372", "Estonia"], ["268", "Eswatini"], ["251", "Ethiopia"],
    ["500", "Falkland Islands"], ["298", "Faroe Islands"], ["679", "Fiji"], ["358", "Finland"],
    ["33", "France"], ["594", "French Guiana"], ["689", "French Polynesia"], ["241", "Gabon"],
    ["220", "Gambia"], ["995", "Georgia"], ["49", "Germany"], ["233", "Ghana"], ["350", "Gibraltar"],
    ["30", "Greece"], ["299", "Greenland"], ["1473", "Grenada"], ["590", "Guadeloupe"], ["1671", "Guam"],
    ["502", "Guatemala"], ["44", "United Kingdom / Guernsey"], ["224", "Guinea"], ["245", "Guinea-Bissau"],
    ["592", "Guyana"], ["509", "Haiti"], ["504", "Honduras"], ["852", "Hong Kong"], ["36", "Hungary"],
    ["354", "Iceland"], ["91", "India"], ["62", "Indonesia"], ["98", "Iran"], ["964", "Iraq"],
    ["353", "Ireland"], ["972", "Israel"], ["39", "Italy"], ["225", "Ivory Coast"], ["1876", "Jamaica"],
    ["81", "Japan"], ["962", "Jordan"], ["7", "Kazakhstan / Russia"], ["254", "Kenya"], ["686", "Kiribati"],
    ["965", "Kuwait"], ["996", "Kyrgyzstan"], ["856", "Laos"], ["371", "Latvia"], ["961", "Lebanon"],
    ["266", "Lesotho"], ["231", "Liberia"], ["218", "Libya"], ["423", "Liechtenstein"], ["370", "Lithuania"],
    ["352", "Luxembourg"], ["853", "Macau"], ["389", "North Macedonia"], ["261", "Madagascar"],
    ["265", "Malawi"], ["60", "Malaysia"], ["960", "Maldives"], ["223", "Mali"], ["356", "Malta"],
    ["692", "Marshall Islands"], ["596", "Martinique"], ["222", "Mauritania"], ["230", "Mauritius"],
    ["262", "Mayotte / Réunion"], ["52", "Mexico"], ["691", "Micronesia"], ["373", "Moldova"],
    ["377", "Monaco"], ["976", "Mongolia"], ["382", "Montenegro"], ["1664", "Montserrat"],
    ["212", "Morocco"], ["258", "Mozambique"], ["95", "Myanmar"], ["264", "Namibia"], ["674", "Nauru"],
    ["977", "Nepal"], ["31", "Netherlands"], ["687", "New Caledonia"], ["64", "New Zealand"],
    ["505", "Nicaragua"], ["227", "Niger"], ["234", "Nigeria"], ["683", "Niue"], ["850", "North Korea"],
    ["1670", "Northern Mariana Islands"], ["47", "Norway"], ["968", "Oman"], ["92", "Pakistan"],
    ["680", "Palau"], ["970", "Palestine"], ["507", "Panama"], ["675", "Papua New Guinea"],
    ["595", "Paraguay"], ["51", "Peru"], ["63", "Philippines"], ["48", "Poland"], ["351", "Portugal"],
    ["1787", "Puerto Rico"], ["974", "Qatar"], ["40", "Romania"], ["250", "Rwanda"],
    ["590", "Saint Barthélemy"], ["290", "Saint Helena"], ["1869", "Saint Kitts and Nevis"],
    ["1758", "Saint Lucia"], ["508", "Saint Pierre and Miquelon"], ["1784", "Saint Vincent and the Grenadines"],
    ["685", "Samoa"], ["378", "San Marino"], ["239", "São Tomé and Príncipe"], ["966", "Saudi Arabia"],
    ["221", "Senegal"], ["381", "Serbia"], ["248", "Seychelles"], ["232", "Sierra Leone"],
    ["65", "Singapore"], ["1721", "Sint Maarten"], ["421", "Slovakia"], ["386", "Slovenia"],
    ["677", "Solomon Islands"], ["252", "Somalia"], ["27", "South Africa"], ["82", "South Korea"],
    ["211", "South Sudan"], ["34", "Spain"], ["94", "Sri Lanka"], ["249", "Sudan"], ["597", "Suriname"],
    ["46", "Sweden"], ["41", "Switzerland"], ["963", "Syria"], ["886", "Taiwan"], ["992", "Tajikistan"],
    ["255", "Tanzania"], ["66", "Thailand"], ["228", "Togo"], ["690", "Tokelau"], ["676", "Tonga"],
    ["1868", "Trinidad and Tobago"], ["216", "Tunisia"], ["90", "Turkey"], ["993", "Turkmenistan"],
    ["1649", "Turks and Caicos Islands"], ["688", "Tuvalu"], ["256", "Uganda"], ["380", "Ukraine"],
    ["971", "United Arab Emirates"], ["598", "Uruguay"], ["998", "Uzbekistan"], ["678", "Vanuatu"],
    ["58", "Venezuela"], ["84", "Vietnam"], ["681", "Wallis and Futuna"], ["967", "Yemen"],
    ["260", "Zambia"], ["263", "Zimbabwe"]
  ];
  var ISO = {"93":"af","355":"al","213":"dz","1684":"as","376":"ad","244":"ao","1264":"ai","1268":"ag","54":"ar","374":"am","297":"aw","61":"au","43":"at","994":"az","1242":"bs","973":"bh","880":"bd","1246":"bb","375":"by","32":"be","501":"bz","229":"bj","1441":"bm","975":"bt","591":"bo","387":"ba","267":"bw","55":"br","246":"io","1284":"vg","673":"bn","359":"bg","226":"bf","257":"bi","855":"kh","237":"cm","1":"us","238":"cv","1345":"ky","236":"cf","235":"td","56":"cl","86":"cn","57":"co","269":"km","243":"cd","242":"cg","682":"ck","506":"cr","385":"hr","53":"cu","599":"cw","357":"cy","420":"cz","45":"dk","253":"dj","1767":"dm","1809":"do","670":"tl","593":"ec","20":"eg","503":"sv","240":"gq","291":"er","372":"ee","268":"sz","251":"et","500":"fk","298":"fo","679":"fj","358":"fi","33":"fr","594":"gf","689":"pf","241":"ga","220":"gm","995":"ge","49":"de","233":"gh","350":"gi","30":"gr","299":"gl","1473":"gd","590":"gp","1671":"gu","502":"gt","44":"gb","224":"gn","245":"gw","592":"gy","509":"ht","504":"hn","852":"hk","36":"hu","354":"is","91":"in","62":"id","98":"ir","964":"iq","353":"ie","972":"il","39":"it","225":"ci","1876":"jm","81":"jp","962":"jo","7":"kz","254":"ke","686":"ki","965":"kw","996":"kg","856":"la","371":"lv","961":"lb","266":"ls","231":"lr","218":"ly","423":"li","370":"lt","352":"lu","853":"mo","389":"mk","261":"mg","265":"mw","60":"my","960":"mv","223":"ml","356":"mt","692":"mh","596":"mq","222":"mr","230":"mu","262":"re","52":"mx","691":"fm","373":"md","377":"mc","976":"mn","382":"me","1664":"ms","212":"ma","258":"mz","95":"mm","264":"na","674":"nr","977":"np","31":"nl","687":"nc","64":"nz","505":"ni","227":"ne","234":"ng","683":"nu","850":"kp","1670":"mp","47":"no","968":"om","92":"pk","680":"pw","970":"ps","507":"pa","675":"pg","595":"py","51":"pe","63":"ph","48":"pl","351":"pt","1787":"pr","974":"qa","40":"ro","250":"rw","290":"sh","1869":"kn","1758":"lc","508":"pm","1784":"vc","685":"ws","378":"sm","239":"st","966":"sa","221":"sn","381":"rs","248":"sc","232":"sl","65":"sg","1721":"sx","421":"sk","386":"si","677":"sb","252":"so","27":"za","82":"kr","211":"ss","34":"es","94":"lk","249":"sd","597":"sr","46":"se","41":"ch","963":"sy","886":"tw","992":"tj","255":"tz","66":"th","228":"tg","690":"tk","676":"to","1868":"tt","216":"tn","90":"tr","993":"tm","1649":"tc","688":"tv","256":"ug","380":"ua","971":"ae","598":"uy","998":"uz","678":"vu","58":"ve","84":"vn","681":"wf","967":"ye","260":"zm","263":"zw"};
  var BY_CODE_LEN_DESC = CODES.slice().sort(function (a, b) { return b[0].length - a[0].length; });
  var DATALIST_ID = "phone-codes-list";

  function qs(sel, root) { return (root || document).querySelector(sel); }
  function qsa(sel, root) { return Array.prototype.slice.call((root || document).querySelectorAll(sel)); }

  function ensureDatalist() {
    if (document.getElementById(DATALIST_ID)) return;
    var dl = document.createElement("datalist");
    dl.id = DATALIST_ID;
    CODES.forEach(function (c) {
      var o = document.createElement("option");
      o.value = c[0];
      o.label = c[1] + " (+" + c[0] + ")";
      dl.appendChild(o);
    });
    document.body.appendChild(dl);
  }

  // Splits a stored "+<digits>" value into [countryCode, nationalNumber]
  // using the longest matching known calling code (1-4 digits). Falls back
  // to a plain 2-digit guess if nothing matches, so an unrecognized/future
  // code still round-trips into two editable fields instead of one blob.
  function splitE164(value) {
    var digits = String(value || "").replace(/\D/g, "");
    if (!digits) return ["", ""];
    for (var i = 0; i < BY_CODE_LEN_DESC.length; i++) {
      var code = BY_CODE_LEN_DESC[i][0];
      if (digits.indexOf(code) === 0 && digits.length > code.length) {
        return [code, digits.slice(code.length)];
      }
    }
    return digits.length > 2 ? [digits.slice(0, 2), digits.slice(2)] : [digits, ""];
  }


  function resolveIso(codeDigits, nationalDigits) {
    for (var n = Math.min(4, codeDigits.length); n >= 1; n--) {
      var d = codeDigits.slice(0, n);
      if (ISO[d]) {
        if (d === "7" && !/^[67]/.test(nationalDigits)) return "ru";
        return ISO[d];
      }
    }
    return "";
  }

  var NAMES = {};
  CODES.forEach(function (c) { NAMES[c[0]] = c[1]; });

  var groupsByHiddenId = {};

  // Wires one phone-input-group element: a free-text
  // <input class="phone-country-code"> (digits only, no leading +), an
  // <input class="phone-national"> and a hidden field the rest of the app
  // already reads/writes (identified by hiddenSelector). Call once per group
  // after it's in the DOM.
  function wireGroup(group, hiddenSelector) {
    if (!group || group.dataset.phoneWired === "1") return;
    var code = qs(".phone-country-code", group);
    var national = qs(".phone-national", group);
    var hidden = qs(hiddenSelector) || qs(".phone-hidden", group);
    if (!code || !national || !hidden) {
      console.error("phone-input.js: wireGroup missing a required element", { hasCode: !!code, hasNational: !!national, hasHidden: !!hidden });
      return;
    }
    group.dataset.phoneWired = "1";
    ensureDatalist();
    code.setAttribute("list", DATALIST_ID);

    var flag = document.createElement("img");
    flag.className = "phone-flag";
    flag.alt = "";
    flag.width = 20;
    flag.height = 15;
    flag.hidden = true;
    flag.addEventListener("error", function () { flag.hidden = true; });
    group.insertBefore(flag, group.firstChild);

    function updateFlag() {
      var iso = resolveIso(code.value.replace(/\D/g, ""), national.value.replace(/\D/g, ""));
      group.dataset.phoneCountry = iso.toUpperCase();
      if (!iso) {
        flag.hidden = true;
        flag.removeAttribute("src");
        return;
      }
      var src = FLAG_BASE + iso + ".svg";
      if (flag.getAttribute("src") !== src) flag.setAttribute("src", src);
      flag.title = NAMES[code.value.replace(/\D/g, "")] || iso.toUpperCase();
      flag.hidden = false;
    }

    var syncing = false;

    function reflect() {
      if (syncing) return;
      var raw = String(hidden.value || "").trim();
      var parts = raw && raw.charAt(0) !== "+" ? ["", raw] : splitE164(raw);
      code.value = parts[0] || "";
      national.value = parts[1] || "";
      updateFlag();
    }
    reflect();

    function sync() {
      syncing = true;
      var codeDigits = code.value.replace(/\D/g, "");
      var natDigits = national.value.replace(/\D/g, "");
      updateFlag();
      hidden.value = codeDigits && natDigits ? ("+" + codeDigits + natDigits) : "";
      try { hidden.dispatchEvent(new Event("change", { bubbles: true })); } catch (e) {}
      try { hidden.dispatchEvent(new Event("input", { bubbles: true })); } catch (e) {}
      syncing = false;
    }
    code.addEventListener("input", sync);
    national.addEventListener("input", sync);

    // Some callers (e.g. an async client-prefill lookup) set the hidden
    // field's value programmatically and dispatch input/change themselves -
    // reflect that into the visible pair too, without reacting to the events
    // this group's own sync() just raised.
    hidden.addEventListener("change", reflect);
    hidden.addEventListener("input", reflect);

    if (hidden.id) groupsByHiddenId[hidden.id] = reflect;
  }

  function autoWire(root) {
    qsa(".phone-input-group", root || document).forEach(function (group) {
      var hiddenId = group.getAttribute("data-hidden-id");
      if (!hiddenId) {
        console.error("phone-input.js: .phone-input-group missing data-hidden-id", group);
        return;
      }
      wireGroup(group, "#" + hiddenId);
    });
  }

  // refresh re-reads the hidden field's current value into the visible
  // code+national pair. Call this after setting the hidden field's value
  // programmatically (e.g. populating an existing client's phone number) -
  // plain .value assignment doesn't fire any event the group would see.
  function refresh(hiddenId) {
    hiddenId = hiddenId.replace(/^#/, "");
    var fn = groupsByHiddenId[hiddenId];
    if (fn) fn();
  }

  function countryOf(group) {
    return (group && group.dataset.phoneCountry) || "";
  }

  window.phoneInput = { countryOf: countryOf, wireGroup: wireGroup, autoWire: autoWire, splitE164: splitE164, refresh: refresh, CODES: CODES };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", function () { autoWire(document); });
  } else {
    autoWire(document);
  }
})();
