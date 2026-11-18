-- Night City Frame Lab - game options (Cyber Engine Tweaks mod)
--
-- Frame Lab puts this folder into the game when you set one of the game's own options on its Game
-- options page, or keep one after a trial (Optimise > Game options). options.txt next to this file
-- lists them, one a line: set|<category>|<item>|<value>
--
-- Each one is set when the game starts and again every time V enters the world, because that is when
-- other mods set their options too. While the game runs, options.txt is read again every two seconds:
-- a value changed in Frame Lab is set within moments, and an option taken out of the list gets the
-- value it had before back. If another mod sets one of them again, it is set back a few times and then
-- left alone (Frame Lab shows that).
--
-- Lines "probe|<category>|<item>" name options Frame Lab wants to know the value of. read.txt is
-- where this mod writes them, with the value each one had before this mod set it.
--
-- The options are only set in the running game: nothing is written to the game's files or to the
-- in-game settings, and without this folder every option is the game's own again.

local wants = {}      -- key -> { cat, item, want }   the options to set (options.txt)
local probes = {}     -- key -> { cat, item }         the options to read
local own = {}        -- key -> the value before this mod set it the first time
local other = {}      -- key -> a value another mod set after this mod did (put back on Reset)
local fights = {}     -- key -> how often it had to be set again
local lost = {}       -- key -> given up: another mod keeps setting it
local serial = ""     -- the serial line of the options.txt that was applied last
local lastText = nil  -- options.txt as read last time
local inWorld = false
local sinceWorld = 0
local readDone = false
local tick = 0

local FIGHTS = 5      -- set again at most this many times, then left to the other mod
local EVERY = 2       -- seconds between two reads of options.txt

local function key(cat, item) return cat .. "|" .. item end

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

local function readFile()
  local f = io.open("options.txt", "r")
  if not f then return nil end
  local t = f:read("*a")
  f:close()
  return t
end

local function parse(text)
  local w, p, s = {}, {}, ""
  for l in (text or ""):gmatch("[^\r\n]+") do
    local what, cat, item, value = l:match("^([^|]*)|([^|]*)|([^|]*)|?(.*)$")
    local sn = l:match("^serial|(.*)$")
    if sn then
      s = sn
    elseif what == "set" and cat and cat ~= "" and item and item ~= "" and value ~= "" then
      w[key(cat, item)] = { cat = cat, item = item, want = value }
    elseif what == "probe" and cat and cat ~= "" and item and item ~= "" then
      p[key(cat, item)] = { cat = cat, item = item }
    end
  end
  return w, p, s
end

-- set every wanted option that doesn't have its value; the value it had before is kept once
local function apply()
  for k, o in pairs(wants) do
    if not lost[k] then
      local now = optGet(o.cat, o.item)
      if now ~= nil and not same(now, o.want) then
        if own[k] == nil then own[k] = now end
        optSet(o.cat, o.item, o.want)
      end
    end
  end
end

-- read.txt: what Frame Lab shows on its Game options page
local function writeRead()
  local f = io.open("read.txt", "w")
  if not f then return end
  f:write("serial|" .. serial .. "\n")
  f:write("time|" .. tostring(os.time()) .. "\n")
  local seen = {}
  local function line(k, cat, item)
    if seen[k] then return end
    seen[k] = true
    local now = optGet(cat, item)
    local state = "free"
    local w = wants[k]
    if w then state = (lost[k] or not same(now, w.want)) and "lost" or "set" end
    if now == nil then state = "missing" end
    f:write(cat .. "|" .. item .. "|" .. (now or "") .. "|" .. (own[k] or "") .. "|" .. state .. "\n")
  end
  for k, o in pairs(wants) do line(k, o.cat, o.item) end
  for k, o in pairs(probes) do line(k, o.cat, o.item) end
  f:close()
end

-- options.txt changed while the game runs: set the new values, give the ones taken out their value back
local function reload(text)
  local w, p, s = parse(text)
  for k, o in pairs(wants) do
    if not w[k] then
      local back = other[k] or own[k]
      if back ~= nil then optSet(o.cat, o.item, back) end
      own[k], other[k], fights[k], lost[k] = nil, nil, nil, nil
    end
  end
  for k, o in pairs(w) do
    local was = wants[k]
    if was and not same(was.want, o.want) then fights[k], lost[k] = nil, nil end
  end
  wants, probes, serial = w, p, s
  apply()
end

-- another mod set one of the options again: set it back, a few times at most
local function hold()
  for k, o in pairs(wants) do
    if not lost[k] then
      local now = optGet(o.cat, o.item)
      if now ~= nil and not same(now, o.want) then
        other[k] = now
        fights[k] = (fights[k] or 0) + 1
        if fights[k] > FIGHTS then
          lost[k] = true
        else
          optSet(o.cat, o.item, o.want)
        end
        readDone = false
      end
    end
  end
end

registerForEvent("onInit", function()
  local ok, text = pcall(readFile)
  if ok and text then
    lastText = text
    wants, probes, serial = parse(text)
  end
  pcall(apply)
  pcall(ObserveAfter, "PlayerPuppet", "OnGameAttached", function()
    inWorld, sinceWorld, readDone = true, 0, false
    pcall(apply)
  end)
  pcall(ObserveAfter, "PlayerPuppet", "OnDetach", function()
    inWorld = false
  end)
end)

registerForEvent("onUpdate", function(delta)
  tick = tick + (delta or 0)
  if inWorld then sinceWorld = sinceWorld + (delta or 0) end
  if tick < EVERY then return end
  tick = 0
  local ok, text = pcall(readFile)
  if ok and text and text ~= lastText then
    lastText = text
    pcall(reload, text)
    readDone = false
  end
  pcall(hold)
  -- the values are read once V is in the world for a few seconds (every mod has set its own by then),
  -- and again after every change
  if not readDone and (sinceWorld > 6 or not inWorld) then
    readDone = true
    pcall(writeRead)
  end
end)
