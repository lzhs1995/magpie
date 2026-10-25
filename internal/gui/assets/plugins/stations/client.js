// The Stations page: the gateways on this computer, a magpie of its own
// each. + makes one, names it, gives it a port and a folder, and hands
// back the one line that starts it; Open goes to that station's own page,
// where everything a single magpie offers is offered, because it is one.
//
// A page the Stations plugin adds to the window (kernel.js), its words in
// i18n.js and its styles in stations.css beside this file. Its routes are
// the plugin's own, under api/plugins/x/stations/ (pluginrt.Ctx.Handle).
magpie.plugin("stations", (ctx) => {
  const API = "plugins/x/stations/";
  let state = null; // the last list the routes gave back
  let adding = null; // the + form's values while it is open, else null

  // ask is one of the page's routes; every one answers with the whole
  // list, so the page draws what it was given and keeps no idea of its
  // own about what is running.
  async function ask(path, body) {
    const data = await api(API + path, body);
    if (data && Array.isArray(data.stations)) state = data;
    return data;
  }

  function el(tag, cls, text) {
    const n = document.createElement(tag);
    if (cls) n.className = cls;
    if (text !== undefined) n.textContent = text;
    return n;
  }

  function button(label, cls, onclick) {
    const b = el("button", cls, t(label));
    b.type = "button";
    b.onclick = onclick;
    return b;
  }

  // copyRow is a line the reader copies: the label, the line itself, and
  // a button that puts it on the clipboard.
  function copyRow(label, value) {
    const row = el("div", "st-row");
    row.append(el("label", null, t(label)));
    const code = el("code", null, value);
    row.append(code);
    const copy = button("Copy", "text", async () => {
      try {
        await navigator.clipboard.writeText(value);
        copy.textContent = t("Copied");
        setTimeout(() => { copy.textContent = t("Copy"); }, 1400);
      } catch (e) {
        status(e.message, "err");
      }
    });
    row.append(copy);
    return row;
  }

  function card(s) {
    const c = el("div", "st-card");
    c.dataset.state = s.state;

    const top = el("div", "st-top");
    top.append(el("span", "st-name", s.name));
    const chip = el("span", "st-chip", t(s.state));
    chip.dataset.state = s.state;
    top.append(chip);
    if (s.protected) {
      const lock = el("span", "st-lock", t("protected"));
      lock.title = t("Protected stations are started and stopped where they were started, not from here.");
      top.append(lock);
    }
    if (s.note) top.append(el("span", "st-note", s.note));

    const acts = el("div", "st-acts");
    if (s.state === "up" && s.webURL) {
      // Open is a tab of this window, where the window holds tabs (macOS,
      // stationtab.go): a station is a magpie of its own on its own port,
      // so its page is loaded top level, in a window of its own that the
      // system keeps in this one's tab group. Its sign-in cookie is that
      // port's, which a frame in this page could not carry. Anywhere else,
      // and in `magpie web`, the answer is {tabbed: false} and the link
      // opens in the browser, as it did before.
      acts.append(button("Open", "text", async () => {
        try {
          const { url } = await api(API + "open?name=" + encodeURIComponent(s.name));
          const tab = await api("station-tab", { name: s.name, url });
          if (!tab || !tab.tabbed) await api("open", { url });
        } catch (e) { status(e.message, "err"); }
      }));
    }
    if (s.state === "down") {
      acts.append(button("Start", "text", () => act("start", { name: s.name })));
    } else if (s.state !== "busy" && !s.protected) {
      acts.append(button("Stop", "text", () => act("stop", { name: s.name })));
    }
    acts.append(button("Edit", "text", () => { adding = { ...s, was: s.name }; draw(); }));
    if (!s.protected) {
      // a first click asks and a second removes: the window's web view
      // shows no confirm() of the page's (it answers false at once), so the
      // question is the button's own, as the profile chips' × asks
      const rm = button("Remove", "text st-rm", () => {
        if (!rm.dataset.armed) {
          rm.dataset.armed = "1";
          rm.textContent = t("Remove {name}? Click again", { name: s.name });
          rm.title = t("Remove {name}? Its settings folder stays where it is.", { name: s.name });
          rm._t = setTimeout(disarm, 4000);
          return;
        }
        disarm();
        act("remove", { name: s.name });
      });
      rm.setAttribute("aria-live", "polite");
      const disarm = () => {
        clearTimeout(rm._t);
        delete rm.dataset.armed;
        rm.textContent = t("Remove");
        rm.title = "";
      };
      acts.append(rm);
    }
    top.append(acts);
    c.append(top);

    const rows = el("div", "st-rows");
    rows.append(copyRow("Start it with", s.launch));
    rows.append(copyRow("Point a client at", s.baseURL));
    rows.append(copyRow("Settings folder", s.folder));
    // the same start written out, for a reader who wants no stations.yml
    // in it: folded away, since the short line is what they normally keep
    if (s.full && s.full !== s.launch) {
      const fold = el("details", "st-full");
      fold.append(el("summary", null, t("Without stations.yml")));
      fold.append(copyRow("Start it with", s.full));
      rows.append(fold);
    }
    c.append(rows);

    if (s.why) c.append(el("p", "st-why", s.why));
    return c;
  }

  async function act(path, body) {
    try {
      await ask(path, body);
      adding = null;
      draw();
    } catch (e) {
      if (adding) { adding.error = e.message; draw(); }
      else status(e.message, "err");
    }
  }

  // form is +, and Edit of a station that is already there. The folder a
  // station keeps its settings in isn't a field: a new one gets a folder
  // named after it, and a renamed one keeps the folder it had, so no
  // rename leaves a gateway's keys behind (internal/stations Edit).
  function form() {
    const f = el("form", "st-form");
    const editing = !!adding.was;
    const fields = el("div", "st-fields");
    const field = (label, name, type, value, extra) => {
      const l = el("label", "st-field");
      l.append(el("span", null, t(label)));
      const i = document.createElement("input");
      i.type = type;
      i.name = name;
      i.value = value ?? "";
      if (extra) Object.assign(i, extra);
      l.append(i);
      fields.append(l);
      return i;
    };
    const name = field("Name", "name", "text", adding.name, { spellcheck: false, autocomplete: "off" });
    const port = field("Gateway port", "port", "number", adding.port || state?.next?.port || "");
    const web = field("Page port", "webPort", "number", adding.webPort || state?.next?.webPort || "");
    const note = field("Note", "note", "text", adding.note);
    f.append(fields);

    const foot = el("div", "st-foot");
    if (adding.error) foot.append(el("span", "st-err", adding.error));
    const go = button(editing ? "Save" : "Create", "primary st-go", () => f.requestSubmit());
    foot.append(button("Cancel", "text", () => { adding = null; draw(); }), go);
    f.append(foot);

    f.onsubmit = (ev) => {
      ev.preventDefault();
      const body = {
        name: name.value.trim(),
        port: Number(port.value),
        webPort: Number(web.value) || 0,
        noWeb: !Number(web.value),
        note: note.value.trim(),
      };
      if (editing) body.was = adding.was;
      act(editing ? "edit" : "new", body);
    };
    // the reader's own name first, where the caret already is
    setTimeout(() => name.focus(), 0);
    return f;
  }

  function draw() {
    const main = document.getElementById("view-stations");
    if (!main) return;
    const list = main.querySelector(".st-list");
    const forms = main.querySelector(".st-forms");
    forms.replaceChildren();
    if (adding) forms.append(form());
    list.replaceChildren();
    const rows = state?.stations || [];
    if (!rows.length && !adding) {
      list.append(el("div", "st-empty", t("No stations yet. + makes the first one.")));
      return;
    }
    for (const s of rows) list.append(card(s));
  }

  async function load() {
    await ask("list");
    draw();
  }

  ctx.slots.add("page", {
    key: "stations",
    title: "Stations",
    html: `
  <div class="st-head">
    <div>
      <span class="label" data-t>Stations</span>
      <p data-t>The gateways on this computer, a magpie of its own each.</p>
      <p data-t>Everything a single magpie offers is on its own page: it is a single magpie, serving its own folder.</p>
    </div>
    <button class="primary st-new" id="stNew" type="button" data-t>New station</button>
  </div>
  <div class="st-forms"></div>
  <div class="st-list"></div>
`,
    mount(main) {
      main.querySelector("#stNew").onclick = () => {
        adding = { name: "", port: 0, webPort: 0, note: "" };
        draw();
      };
    },
    open() { return load(); },
    // the window came back, or Refresh: a station the reader started or
    // stopped elsewhere shows its own state here
    refresh() { return load(); },
    reload() { return load(); },
    relang() { draw(); },
  });
});
