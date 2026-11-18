-- Night City Frame Lab - session markers (Cyber Engine Tweaks mod, game 2.5 and later)
--
-- Frame Lab puts this folder into the game for one recording session and takes it out again
-- afterwards. It writes one line to markers.log (next to this file) when a loading screen starts or
-- ends and when V enters or leaves the game world, so the report can leave loading screens and the
-- main menu out of the stutter list. Every 5 seconds of play it also writes where V is (district, on
-- foot or in a vehicle, in a fight or not), which is how Frame Lab knows which test route a
-- recording covers. Every line goes to disk straight away, so nothing is lost when the game closes
-- badly.
--
-- In a test recording Frame Lab also puts routes.txt here: the routes still to record. The mod then
-- counts them the same way Frame Lab will and shows one small line of progress on screen, so you can
-- see how much is left without quitting.
--
-- In a test recording Frame Lab may also put options.txt here. A line "probe|<category>|<item>" names
-- one of the game's own options (the ones mods set through Cyber Engine Tweaks): its value is read once
-- and written to markers.log, so Frame Lab knows what this version of the game has. A line
-- "set|<category>|<item>|<value>" is an option on trial: it is set when the game starts and again when
-- V enters the world, and every 5 seconds it is checked that nothing else has changed it. The option is
-- only set in the running game: nothing is written to the game's files, and at the next start it is
-- the game's own again.
--
-- Apart from an option on trial, the mod only reads and draws that one line.

local out = nil          -- markers.log
local inGame = false     -- V is in the game world
local loadingAt = 0      -- os.time() when a loading screen came up, 0 = none
local acc = 0            -- seconds since the last position line
local lastSim = nil      -- game clock at the last position check (it stands still in menus)
local failed = false     -- a position read failed and was reported once

local EVERY = 5          -- seconds between position lines
local MARGIN = 10        -- a route is called recorded this many seconds late, so Frame Lab is sure to agree

local routes = {}        -- the routes still to record (from routes.txt); empty in a normal session
local line = nil         -- the progress line on screen, nil = nothing to show
local lineUntil = 0      -- os.time() after which the line goes away (0 = stays)
local flags = nil        -- window flags of the line, nil = drawing is off

local probes = {}        -- the game's options to read once (options.txt)
local trials = {}        -- options on trial in this recording (options.txt)
local probed = false     -- the options were read
local trialNote = nil    -- what the line on screen says about the trial

local function say(text)
  if not out then
    out = io.open("markers.log", "a")
    if not out then return end
  end
  out:write(string.format("%d\t%s\n", os.time(), text))
  out:flush()
end

-- a game object that is really there (an emptied weak reference is not nil in Lua)
local function there(x)
  if x == nil then return false end
  if type(IsDefined) == "function" then
    local ok, yes = pcall(IsDefined, x)
    if ok then return yes and true or false end
  end
  return true
end

-- the first of these ways of asking that works (the game's script names differ a little between versions)
local function first(...)
  for _, fn in ipairs({ ... }) do
    local ok, v = pcall(fn)
    if ok and v ~= nil then return v end
  end
  return nil
end

local function simTime()
  local t = first(
    function() return Game.GetTimeSystem():GetSimTime():ToFloat() end,
    function() return Game.GetSimTime():ToFloat() end,
    function() return EngineTime.ToFloat(Game.GetSimTime()) end)
  if type(t) == "number" then return t end
  return nil
end

-- a district record's name as the game's scripts spell it ("LittleChina", "Watson", "Dogtown")
local function recName(rec)
  local n = first(
    function() return rec:EnumName() end,
    function() return rec:Type().value end)
  if n == nil or tostring(n) == "" then return "-" end
  return tostring(n)
end

-- where V is: the district and the ones it is in (at most three), in a vehicle or not, in a fight or not
local function where()
  local p = Game.GetPlayer()
  if not there(p) then return nil end
  local chain = {}
  local ps = first(
    function() return Game.GetScriptableSystemsContainer():Get("PreventionSystem") end,
    function() return p:GetPreventionSystem() end)
  if there(ps) then
    local d = first(
      function() return ps:GetCurrentDistrict() end,
      function() return ps.districtManager:GetCurrentDistrict() end)
    if there(d) then
      local rec = first(function() return d:GetDistrictRecord() end)
      while there(rec) and #chain < 3 do
        chain[#chain + 1] = recName(rec)
        local up = rec
        rec = first(function() return up:ParentDistrict() end)
      end
    end
  end
  local veh = first(
    function() return Game["GetMountedVehicle;GameObject"](p) end,
    function() return Game.GetMountedVehicle(p) end)
  local car = there(veh)
  local fight = first(function() return p:IsInCombat() end) == true
  return chain, car, fight
end

-- "at <district> / <the district it is in> [/ <the one above that>] <foot|car> <fight|calm>"
local function placeLine(chain, car, fight)
  local names = #chain > 0 and table.concat(chain, " / ") or "-"
  return "at " .. names .. " " .. (car and "car" or "foot") .. " " .. (fight and "fight" or "calm")
end

---------------------------------------------------------------------------------------------------
-- the game's options: read once, and one may be on trial

local function key(o)
  return o.cat .. "|" .. o.item
end

-- an option's value as the game gives it ("true", "400", "0.250000"); nil when the game has no such option
local function optGet(cat, item)
  local ok, v = pcall(function() return GameOptions.Get(cat, item) end)
  if not ok or v == nil then return nil end
  v = tostring(v)
  if v == "" then return nil end
  return v
end

-- the same value (numbers are compared as numbers: the game writes 0.25 as 0.250000)
local function same(a, b)
  if a == nil or b == nil then return false end
  if a == b then return true end
  local x, y = tonumber(a), tonumber(b)
  if x and y then
    return math.abs(x - y) <= 0.0001 * math.max(1, math.abs(x), math.abs(y))
  end
  return false
end

-- sets an option to "true", "false" or a number. A number is tried as the kind the game shows it as
-- first; Cyber Engine Tweaks refuses the wrong kind and changes nothing, so the other kind is tried then.
local function optSet(cat, item, want)
  if want == "true" or want == "false" then
    pcall(function() GameOptions.SetBool(cat, item, want == "true") end)
    return
  end
  local n = tonumber(want)
  if not n then return end
  local whole = math.floor(n + 0.5)
  local now = optGet(cat, item)
  local asFloat = (now ~= nil and now:find(".", 1, true) ~= nil) or want:find(".", 1, true) ~= nil
  local function setFloat() pcall(function() GameOptions.SetFloat(cat, item, n) end) end
  local function setInt() pcall(function() GameOptions.SetInt(cat, item, whole) end) end
  if asFloat then
    setFloat()
    if n == whole and not same(optGet(cat, item), want) then setInt() end
  else
    setInt()
    if not same(optGet(cat, item), want) then setFloat() end
  end
end

-- options.txt: probe|category|item  and  set|category|item|value
local function loadOptions()
  local f = io.open("options.txt", "r")
  if not f then return end
  for l in f:lines() do
    l = l:gsub("[\r\n]", "")
    local what, cat, item, value = l:match("^([^|]*)|([^|]*)|([^|]*)|?(.*)$")
    if cat and cat ~= "" and item and item ~= "" then
      if what == "probe" then
        probes[#probes + 1] = { cat = cat, item = item }
      elseif what == "set" and value ~= "" then
        trials[#trials + 1] = { cat = cat, item = item, want = value }
      end
    end
  end
  f:close()
end

local function trialLine()
  trialNote = nil
  for _, t in ipairs(trials) do
    if t.off then
      trialNote = "Option on trial: " .. t.item .. " is NOT set (" .. t.off .. "). This recording won't count for the trial."
    elseif not trialNote then
      trialNote = "Option on trial: " .. t.item .. " = " .. t.want
    end
  end
end

-- puts the options on trial in place: when the game starts (first) and whenever V enters the world,
-- because that is when other mods set their options too
local function setTrials(first)
  for _, t in ipairs(trials) do
    if not t.off then
      local now = optGet(t.cat, t.item)
      if now == nil then
        t.off = "the game has no such option"
        say("trial " .. key(t) .. " missing")
      else
        if first then say("trial " .. key(t) .. " was " .. now .. " want " .. t.want) end
        if same(now, t.want) then
          if first then say("trial " .. key(t) .. " set " .. now) end
        else
          optSet(t.cat, t.item, t.want)
          local got = optGet(t.cat, t.item)
          if same(got, t.want) then
            say("trial " .. key(t) .. (first and " set " or " again ") .. got .. (first and "" or " from " .. now))
          else
            t.off = "the game didn't take the value"
            say("trial " .. key(t) .. " refused " .. tostring(got))
          end
        end
      end
    end
  end
  trialLine()
end

-- every position line: is each option on trial still what it was set to? If something else changed
-- it, it is left alone and reported: a trial has to hold by itself.
local function checkTrials()
  for _, t in ipairs(trials) do
    if not t.off then
      local now = optGet(t.cat, t.item)
      if not same(now, t.want) then
        t.off = "something else changed it to " .. tostring(now)
        say("trial " .. key(t) .. " lost " .. tostring(now))
        trialLine()
      end
    end
  end
end

-- once, at the first position line (every mod has set its options by then): what the game's options are
local function probe()
  local n = 0
  for _, o in ipairs(probes) do
    local v = optGet(o.cat, o.item)
    if v then
      n = n + 1
      say("option " .. key(o) .. " = " .. v)
    end
  end
  say("options read: " .. n .. " of " .. #probes)
end

---------------------------------------------------------------------------------------------------
-- test routes: counted the way Frame Lab counts them

local function norm(s)
  return (tostring(s):lower():gsub("[^a-z0-9]", ""))
end

-- the place is the named district or inside it; "=Name" is exactly that district, "=-" open country
local function inPlace(chain, name)
  if name:sub(1, 1) == "=" then
    local n = norm(name:sub(2))
    if n == "" then return #chain == 0 end
    for _, c in ipairs(chain) do
      if norm(c) == n then return true end
    end
    return false
  end
  local n = norm(name)
  if n == "" then return false end
  for _, c in ipairs(chain) do
    if norm(c):find(n, 1, true) then return true end
  end
  return false
end

local function split(s, sep)
  local t = {}
  for part in (s .. sep):gmatch("(.-)" .. sep) do
    if part ~= "" then t[#t + 1] = part end
  end
  return t
end

-- routes.txt, one route a line: name|seconds|foot, car or any|fight 0/1|places,..|pass through,..|places as text
local function loadRoutes()
  local f = io.open("routes.txt", "r")
  if not f then return end
  for l in f:lines() do
    local name, need, mode, fight, places, both, text = l:match("^([^|]*)|([^|]*)|([^|]*)|([^|]*)|([^|]*)|([^|]*)|([^|]*)")
    local n = tonumber(need)
    if name and name ~= "" and n and n > 0 then
      routes[#routes + 1] = { name = name, need = n, mode = mode, fight = fight == "1", places = split(places, ","),
        both = split(both, ","), text = text or "", secs = 0, passed = {}, done = false }
    end
  end
  f:close()
end

-- V is doing what the route asks for (on foot or driving, in a fight or not), wherever that is
local function kindFits(r, car, fight)
  if r.mode == "car" and not car then return false end
  if r.mode == "foot" and car then return false end
  return r.fight == fight
end

local function placeFits(r, chain)
  if #r.places == 0 then return true end
  for _, n in ipairs(r.places) do
    if inPlace(chain, n) then return true end
  end
  return false
end

local function clock(s)
  s = math.floor(s)
  return string.format("%d:%02d", math.floor(s / 60), s % 60)
end

-- one position line counts 5 seconds for every route it fits; then the line on screen is made again
local function count(chain, car, fight)
  if #routes == 0 then return end
  local parts, left, elsewhere = {}, 0, nil
  for _, r in ipairs(routes) do
    if not r.done and kindFits(r, car, fight) then
      if placeFits(r, chain) then
        r.secs = r.secs + EVERY
        for _, n in ipairs(r.both) do
          if inPlace(chain, n) then r.passed[n] = true end
        end
        local all = true
        for _, n in ipairs(r.both) do
          if not r.passed[n] then all = false end
        end
        if all and r.secs >= r.need + MARGIN then
          r.done = true
          say("route recorded: " .. r.name)
        end
      elseif r.text ~= "" then
        elsewhere = r.name .. " counts in " .. r.text
      end
    end
    if r.done then
      parts[#parts + 1] = r.name .. " recorded"
    else
      left = left + 1
      if r.secs >= r.need then
        -- the time is in, but a route is only called recorded MARGIN seconds late (or a place to pass
        -- through is still missing): say so, so the line doesn't look stuck at "1:00 of 1:00"
        local missing = {}
        for _, n in ipairs(r.both) do
          if not r.passed[n] then missing[#missing + 1] = n end
        end
        if #missing > 0 then
          parts[#parts + 1] = r.name .. " " .. clock(r.need) .. " in, still to pass through " .. table.concat(missing, ", ")
        else
          local more = math.max(EVERY, r.need + MARGIN - r.secs)
          parts[#parts + 1] = r.name .. " almost: " .. (r.fight and "keep fighting" or "keep going") .. " about " .. more .. " s more"
        end
      else
        parts[#parts + 1] = r.name .. " " .. clock(r.secs) .. " of " .. clock(r.need)
      end
    end
  end
  if left == 0 then
    if lineUntil == 0 then
      line = "Frame Lab test: everything is recorded. Quit the game when you like."
      lineUntil = os.time() + 30
    end
    return
  end
  line = "Frame Lab test:  " .. table.concat(parts, "   |   ")
  if elsewhere then line = line .. "\n" .. elsewhere end
  if trialNote then line = line .. "\n" .. trialNote end
end

---------------------------------------------------------------------------------------------------

registerForEvent("onInit", function()
  say("markers loaded")
  local oko, erro = pcall(loadOptions)
  if not oko then
    probes, trials = {}, {}
    say("options not read: " .. tostring(erro))
  end
  if #trials > 0 then
    local okt, errt = pcall(setTrials, true)
    if not okt then
      trials = {}
      say("trial not set: " .. tostring(errt))
    end
  end
  local okr, err = pcall(loadRoutes)
  if not okr then
    routes = {}
    say("routes not read: " .. tostring(err))
  elseif #routes > 0 then
    say("routes to record: " .. #routes)
    -- a small line near the top of the screen that takes no clicks and keeps no settings
    local okf, f = pcall(function()
      return ImGuiWindowFlags.NoTitleBar + ImGuiWindowFlags.NoResize + ImGuiWindowFlags.NoMove + ImGuiWindowFlags.NoScrollbar
        + ImGuiWindowFlags.AlwaysAutoResize + ImGuiWindowFlags.NoSavedSettings + ImGuiWindowFlags.NoInputs + ImGuiWindowFlags.NoNav
        + ImGuiWindowFlags.NoFocusOnAppearing + ImGuiWindowFlags.NoBringToFrontOnFocus
    end)
    if okf then flags = f else say("progress line off: " .. tostring(f)) end
  end
  local function watch(how, class, method, fn)
    local ok, e = pcall(how, class, method, fn)
    if not ok then say("could not watch " .. class .. "." .. method .. ": " .. tostring(e)) end
  end
  watch(Observe, "LoadingScreenProgressBarController", "OnInitialize", function()
    loadingAt = os.time()
    say("loading screen on")
  end)
  watch(Observe, "LoadingScreenProgressBarController", "OnUninitialize", function()
    loadingAt = 0
    say("loading screen off")
  end)
  -- menus (2.5): the pause menu, the hub (inventory, map, journal ...), vendors, the death screen. Opening
  -- or closing one can take one long frame; with these lines the report says so instead of "not explained".
  local menuNow = "MenuScenario_Idle"
  local function scenario(x)
    for _, f in ipairs({ function() return Game.NameToString(x) end, function() return NameToString(x) end, function() return tostring(x) end }) do
      local ok, s = pcall(f)
      if ok and type(s) == "string" then
        local m = s:match("MenuScenario_%w+")
        if m then return m end
      end
    end
    return nil
  end
  watch(Observe, "inkMenuScenario", "SwitchToScenario", function(this, name)
    local to = scenario(name)
    if not to or to == menuNow then return end
    if to == "MenuScenario_Idle" then
      say("menu closed")
    elseif inGame then
      say("menu open " .. (to:gsub("^MenuScenario_", "")))
    end
    menuNow = to
  end)
  watch(ObserveAfter, "PlayerPuppet", "OnGameAttached", function()
    inGame = true
    acc = 0
    lastSim = nil
    say("in game")
    if #trials > 0 then pcall(setTrials, false) end
  end)
  watch(Observe, "PlayerPuppet", "OnDetach", function()
    inGame = false
    say("left the game")
  end)
  -- the mods were reloaded while V is already in the world
  local ok, p = pcall(function() return Game.GetPlayer() end)
  if ok and there(p) then
    local ok2, att = pcall(function() return p:IsAttached() end)
    if ok2 and att then
      inGame = true
      say("in game")
    end
  end
end)

registerForEvent("onUpdate", function(dt)
  if not inGame then return end
  acc = acc + (dt or 0)
  if acc < EVERY then return end
  acc = 0
  -- a loading screen is up (one that never reported its end stops counting after 3 minutes)
  if loadingAt ~= 0 and os.time() - loadingAt < 180 then return end
  -- the game clock stands still in the pause menu, the map and the inventory: not play
  local sim = simTime()
  local still = sim ~= nil and lastSim ~= nil and (sim - lastSim) < 0.05
  lastSim = sim
  if still then return end
  if not probed then
    probed = true
    if #probes > 0 then pcall(probe) end
  end
  if #trials > 0 then pcall(checkTrials) end
  local ok, chain, car, fight = pcall(where)
  if ok and chain then
    say(placeLine(chain, car, fight))
    local okc, err = pcall(count, chain, car, fight)
    if not okc and not failed then
      failed = true
      line = nil
      say("routes not counted: " .. tostring(err))
    end
  elseif not ok and not failed then
    failed = true
    say("position not read: " .. tostring(chain))
  end
end)

-- the progress line of a test recording (nothing is drawn in a normal session)
local function drawLine()
  local w, h = GetDisplayResolution()
  ImGui.SetNextWindowPos((w or 1920) * 0.30, (h or 1080) * 0.03, ImGuiCond.Always)
  ImGui.PushStyleColor(ImGuiCol.WindowBg, 0.0, 0.0, 0.0, 0.55)
  ImGui.PushStyleColor(ImGuiCol.Text, 0.86, 0.95, 0.93, 1.0)
  if ImGui.Begin("Frame Lab test recording", flags) then
    ImGui.Text(line)
  end
  ImGui.End()
  ImGui.PopStyleColor(2)
end

registerForEvent("onDraw", function()
  if not line or not flags or not inGame then return end
  if lineUntil ~= 0 and os.time() > lineUntil then
    line = nil
    return
  end
  local ok, err = pcall(drawLine)
  if not ok then
    flags = nil -- never try again: the markers matter more than the line
    say("progress line off: " .. tostring(err))
  end
end)

registerForEvent("onShutdown", function()
  if out then
    out:close()
    out = nil
  end
end)
