-- vim: tabstop=4 shiftwidth=4 expandtab

-- Copyright 2026 HAProxy Technologies LLC
--
-- Licensed under the Apache License, Version 2.0 (the "License");
-- you may not use this file except in compliance with the License.
-- You may obtain a copy of the License at
--
--    http://www.apache.org/licenses/LICENSE-2.0
--
-- Unless required by applicable law or agreed to in writing, software
-- distributed under the License is distributed on an "AS IS" BASIS,
-- WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
-- See the License for the specific language governing permissions and
-- limitations under the License.

-- print a Lua table
function print_r(t, indent)
    indent = indent or ""
    for k, v in pairs(t) do
        local key = core.concat()
        key:add(indent)
        key:add("[")
        key:add(tostring(k))
        key:add("] = ")
        if type(v) == "table" then
            key:add("{")
            print(key:dump())
            print_r(v, indent .. "  ") -- Recursive call with more indentation
            print(indent .. "}")
        else
            key:add(tostring(v))
            print(key:dump())
        end
    end
end
function cli_print_r(t, indent, applet)
    indent = indent or ""
    for k, v in pairs(t) do
        local key = core.concat()
        key:add(indent)
        key:add("[")
        key:add(tostring(k))
        key:add("] = ")
        if type(v) == "table" then
            key:add("{\n")
            applet:send(key:dump())
            cli_print_r(v, indent .. "  ", applet) -- Recursive call with more indentation
            applet:send((indent .. "}\n"))
        else
            key:add(tostring(v))
            key:add("\n")
            applet:send(key:dump())
        end
    end
end

-- count entries into a dictionary table
local function count_keys(tbl)
    local count = 0
    for _ in pairs(tbl) do
        count = count + 1
    end
    return count
end


-- Global cache for parsed route data
-- TODO: might need a "purge" strategy to avoid memory leak
local cache = {}

-- Cache for parsed conditional map values ("~1;..."), see parse_cond.
local cond_cache = {}

-- Compiled map regexes, keyed by pattern, declared here so the cache-clearing
-- task can see it (see get_regex below).
local regex_cache = {}

-- Per-thread path map indexes, maintained by the find_route section below;
-- declared here so the cache-clearing task can see it.
local map_index = {}

-- dictionnary to describe supported algorithms
-- each algorithm must expose a few functions:
-- - load_route: to load a route string in the cache
local algorithms = {}
algorithms["default"] = "wr"
--
-- weighted random
algorithms["wr"] = {}
local function wr_load_route(route_str, txn)
    local route_list_str = txn.c:json_query(route_str, "$.l") or ''
    local route_list_tbl = core.tokenize(route_list_str, ',', true)
    local routes = {}
    local cumulative_weight = 0

    for k,v in ipairs(route_list_tbl) do
        local route_tbl = core.tokenize(v, ':')
        local backend_name = route_tbl[1]
        local weight = tonumber(route_tbl[2])
        cumulative_weight = cumulative_weight + weight
        -- Store the backend with its CUMULATIVE weight. This is key for fast lookups.
        table.insert(routes, { backend = backend_name, cumulative = cumulative_weight })
    end

    -- total weight is equal to cumulative weight of the latest entry in the table
    return { algorithm = "wr", routes = routes, total_weight = cumulative_weight }
end
algorithms["wr"]["load_route"] = wr_load_route
local function wr_find_destination(route_data, txn)
    if not route_data or route_data.total_weight == 0 then
        return
    end

    -- Generate a random number based on the total_weight for this route
    -- Note: rand(n) generates 0 to n-1, so we compare with '<'
    local random_val = txn.f:rand(route_data.total_weight)

    -- Find the correct backend by checking the random value against the cumulative weights
    for _, route in ipairs(route_data.routes) do
        if random_val < route.cumulative then
            txn.set_var(txn, 'txn.backend', route.backend)
            txn.set_var(txn, 'txn.random', random_val)
            return
        end
    end
end
algorithms["wr"]["find_destination"] = wr_find_destination
--
-- failover
--   pick up the first backend from the list which looks "operational"
algorithms["fo"] = {}
local function fo_load_route(route_str, txn)
    local route_list_str = txn.c:json_query(route_str, "$.l") or ''
    local route_list_tbl = core.tokenize(route_list_str, ',', true)
    local routes = {}

    for k,v in ipairs(route_list_tbl) do
        local route_tbl = core.tokenize(v, ':')
        local backend_name = route_tbl[1]
        local min = tonumber(route_tbl[2]) or 1  -- min server is 1 if not provided
        -- Store the backend with its CUMULATIVE weight. This is key for fast lookups.
        table.insert(routes, { backend = backend_name, min = min })
    end

    -- total weight is equal to cumulative weight of the latest entry in the table
    return { algorithm = "fo", routes = routes }
end
algorithms["fo"]["load_route"] = fo_load_route
local function fo_find_destination(route_data, txn)
    if not route_data then
        return
    end

    -- Find the correct backend by checking the random value against the cumulative weights
    for _, route in ipairs(route_data.routes) do
        local backend = core.concat()
        -- check if this backend has enough capacity and use it
        local habackend = core.backends[route.backend] or nil
        if habackend then
            local srv_up = habackend.get_srv_act(habackend)
            -- we can use this backend if current number of available servers
            -- is greater or equal to the minimum number of servers for this destination
            if srv_up >= route.min then
                txn.set_var(txn, 'txn.backend', route.backend)
                return
            end
        end
    end
end
algorithms["fo"]["find_destination"] = fo_find_destination


local function clear_cache()
    while true do
        local count = count_keys(cache)
        local str = core.concat()
        cache = {}
        cond_cache = {}
        regex_cache = {}
        -- Also drop map indexes, so deleted frontends do not leak memory.
        map_index = {}
        -- str:add('cleared ')
        -- str:add(count)
        -- str:add(' entries')
        -- print(str:dump())
        core.sleep(60)
    end
end
core.register_task(clear_cache)

local function cli_clear_cache(applet, arg1, arg2, arg3, arg4)
    local count = count_keys(cache)
    cache = {}
    cond_cache = {}
    regex_cache = {}
    local str = core.concat()
    str:add('cleared ')
    str:add(count)
    str:add(' entries')
    applet:send(str:dump())
end
core.register_cli({"route", "clear", "cache"}, "Clear route cache", cli_clear_cache)

local function cli_dump_cache(applet, arg1, arg2, arg3, arg4)
    cli_print_r(cache, nil, applet)
end
core.register_cli({"route", "dump", "cache"}, "dump route cache", cli_dump_cache)

-- Main function called by HAProxy for each request
function route(txn)
    -- The first argument passed from HAProxy is the route configuration string
    local route_str = txn.f:var("txn.route")
    if not route_str or route_str == "" then
        return
    end

    local algo = txn.c:json_query(route_str, "$.a") or nil
    if algo == nil then
        algo = algorithms.default
    end

    -- THE CACHING LOGIC
    -- If the route string isn't in our cache, parse it and store it.
    if not cache[route_str] then
        -- use relevant routing logic to populate the cache
        cache[route_str] = algorithms[algo].load_route(route_str, txn)
    end

    -- set the destination as a txn.backend variable in HAPRoxy
    algorithms[algo].find_destination(cache[route_str], txn)
end

-- Register the function to be called from HAProxy
core.register_action("route", { "http-req" }, route)

-- ---------------------------------------------------------------------------
-- Match-condition evaluation (headers, method, query params)
--
-- Map values containing at least one conditional candidate are written by the
-- controller as "~1;<conds>>target;<conds>>target;..." with candidates in
-- precedence order, so the first passing candidate is the correct one.
-- Conditions inside a candidate are ANDed; empty conditions make the
-- candidate the unconditional fallback (always last).
-- ---------------------------------------------------------------------------

-- Percent-decode names/values encoded by the controller so map values never
-- contain structural delimiters or whitespace.
local function pct_decode(s)
    return (s:gsub("%%(%x%x)", function(h)
        return string.char(tonumber(h, 16))
    end))
end

local function compile_regex(pattern)
    -- Regex.new returns (status, regex|error); a failed compile never matches.
    local ok, st, re = pcall(Regex.new, pattern, true)
    if ok and st then
        return re
    end
    return nil
end

-- parse_cond turns a "~1;..." value into a list of candidates:
--   { target, nm, nh, nq, conds = { {kind,name,type,value,re} ... } }
-- Results are cached per raw value string.
local function parse_cond(value)
    local cached = cond_cache[value]
    if cached ~= nil then
        return cached
    end
    local cands = {}
    for cand_str in value:gmatch("[^;]+") do
        local cond_str, target = cand_str:match("^(.-)>(.*)$")
        if target ~= nil and target ~= "" then
            local cand = { target = target, nm = false, nh = 0, nq = 0, conds = {} }
            if cond_str ~= "" then
                for c in cond_str:gmatch("[^,]+") do
                    local kind, rest = c:match("^(.)=(.*)$")
                    if kind == "m" then
                        cand.nm = true
                        cand.conds[#cand.conds + 1] = { kind = "m", value = pct_decode(rest) }
                    elseif kind == "h" or kind == "q" then
                        local name, t, val = rest:match("^([^:]+):(%a):(.*)$")
                        if name ~= nil then
                            local cond = {
                                kind = kind,
                                name = pct_decode(name),
                                type = t,
                                value = pct_decode(val),
                            }
                            if t == "r" then
                                cond.re = compile_regex(cond.value)
                                if cond.re == nil then
                                    core.Alert("select_route: cannot compile regex: " .. cond.value)
                                end
                            end
                            if kind == "h" then
                                cand.nh = cand.nh + 1
                            else
                                cand.nq = cand.nq + 1
                            end
                            cand.conds[#cand.conds + 1] = cond
                        end
                    end
                end
            end
            cands[#cands + 1] = cand
        end
    end
    cond_cache[value] = cands
    return cands
end

-- Request context, built lazily so unconditional routes pay nothing.
local function request_headers(txn, ctx)
    if ctx.hdrs == nil then
        local norm = {}
        -- Header names are stored lowercased by HAProxy; normalize anyway so
        -- the lookup cannot miss on case.
        for k, v in pairs(txn.http:req_get_headers()) do
            norm[k:lower()] = v
        end
        ctx.hdrs = norm
    end
    return ctx.hdrs
end

-- Query parameters, decoded (percent-escapes, '+' as space); names are
-- case-sensitive. A repeated parameter matches any occurrence.
local function request_params(txn, ctx)
    if ctx.params == nil then
        local params = {}
        local qs = txn.f:query()
        if qs and qs ~= "" then
            for pair in qs:gmatch("[^&]+") do
                local name, value = pair:match("^([^=]*)=?(.*)$")
                local n = pct_decode(name:gsub("%+", " "))
                local list = params[n]
                if list == nil then
                    list = {}
                    params[n] = list
                end
                list[#list + 1] = pct_decode(value:gsub("%+", " "))
            end
        end
        ctx.params = params
    end
    return ctx.params
end

local function request_method(txn, ctx)
    if ctx.method == nil then
        ctx.method = txn.f:method()
    end
    return ctx.method
end

local function cond_matches(txn, cond, ctx)
    if cond.kind == "m" then
        return request_method(txn, ctx) == cond.value
    end
    local vals
    if cond.kind == "h" then
        vals = request_headers(txn, ctx)[cond.name]
    elseif cond.kind == "q" then
        vals = request_params(txn, ctx)[cond.name]
    else
        return false
    end
    if vals == nil then
        return false
    end
    for _, v in pairs(vals) do
        if cond.type == "r" then
            -- Unanchored search; anchor with ^...$ for exact matches.
            if cond.re ~= nil and cond.re:exec(v) then
                return true
            end
        elseif v == cond.value then
            return true
        end
    end
    return false
end

local function eval_cands(txn, conds, ctx)
    for _, cond in ipairs(conds) do
        if not cond_matches(txn, cond, ctx) then
            return false
        end
    end
    return true
end

-- resolve_value returns the effective target of a raw map value plus the
-- candidate's condition counts, used for cross-candidate precedence scoring.
-- Returns nil when the value is conditional and no candidate passes.
local function resolve_value(txn, value, ctx)
    if value:sub(1, 2) ~= "~1" then
        return { target = value, nm = false, nh = 0, nq = 0 }
    end
    for _, cand in ipairs(parse_cond(value)) do
        if eval_cands(txn, cand.conds, ctx) then
            return { target = cand.target, nm = cand.nm, nh = cand.nh, nq = cand.nq }
        end
    end
    return nil
end


-- Path maps are pre-loaded by HAProxy via dummy ACLs in haproxy.cfg, so
-- core.get_patref returns a handle to the live in-memory pat_ref.
-- Iterating the patref costs ~0.4us per entry (each pairs() step crosses
-- into C), so lookups are served from per-thread indexes rebuilt from the
-- patref when older than MAP_INDEX_TTL. The patref exposes no change
-- signal, so runtime map updates become visible within the TTL window
-- instead of on the next request.
local MAP_INDEX_TTL = 1

local patref_cache = {}

-- get_regex returns the compiled form of a map regex pattern, from the
-- regex_cache declared at the top of the file. Map keys are POSIX ERE
-- written by the controller; Lua patterns cannot express them (no
-- alternation), so the HAProxy engine must compile and run them. A failed
-- compile is cached as false and never matches.
local function get_regex(pattern)
    local cached = regex_cache[pattern]
    if cached ~= nil then
        if cached == false then return nil end
        return cached
    end
    local ok, st, re = pcall(Regex.new, pattern, true)
    if ok and st then
        regex_cache[pattern] = re
        return re
    end
    regex_cache[pattern] = false
    core.Warning("find_route: cannot compile path regex: " .. pattern)
    return nil
end

local function get_ref(filepath)
    local r = patref_cache[filepath]
    if r ~= nil then
        if r == false then return nil end
        return r
    end
    local ok, ref = pcall(core.get_patref, filepath)
    if ok and ref then
        patref_cache[filepath] = ref
        return ref
    end
    -- Sentinel so we alert once per filepath, not per request.
    patref_cache[filepath] = false
    core.Alert("find_route: map not registered with HAProxy: " .. filepath)
    return nil
end

local function now_s()
    local t = core.now()
    return t.sec + t.usec / 1e6
end

-- map_index[filepath] = { built_at, values, sorted }
local function build_index(filepath)
    local ref = get_ref(filepath)
    if not ref then return nil end
    local idx = { built_at = now_s(), values = {}, sorted = {}, maxlen = 0 }
    for k, v in pairs(ref) do
        idx.values[k] = v
        idx.sorted[#idx.sorted + 1] = k
        if #k > idx.maxlen then idx.maxlen = #k end
    end
    table.sort(idx.sorted)
    return idx
end

local function index_for(filepath, now)
    local idx = map_index[filepath]
    if idx ~= nil and now - idx.built_at < MAP_INDEX_TTL then
        return idx
    end
    local fresh = build_index(filepath)
    if fresh ~= nil then
        map_index[filepath] = fresh
        return fresh
    end
    return idx
end

local function exact_lookup(filepath, key, now)
    local idx = index_for(filepath, now)
    if not idx then return nil end
    return idx.values[key]
end

-- Scoring constants: exact > any prefix length > regex > nothing.
local SCORE_EXACT = 1e9
local SCORE_REGEX = -1

-- Walk the three path maps for one listener-route candidate in specificity
-- order (exact, prefixes longest-first, regex), returning the first entry
-- whose value is either unconditional or has a passing conditional candidate.
-- Failed conditional entries fall through to less specific entries, as the
-- spec requires (a non-matching rule must not shadow a less specific one).
local function resolve_passing(txn, files, blr, lr_len, ctx, now)
    local v = exact_lookup(files.exact, blr, now)
    local r = v and resolve_value(txn, v, ctx)
    if r then
        r.score = SCORE_EXACT
        return r
    end

    local idx = index_for(files.prefix, now)
    if idx ~= nil then
        local maxlen = idx.maxlen
        if maxlen > #blr then
            maxlen = #blr
        end
        for len = maxlen, 1, -1 do
            v = idx.values[blr:sub(1, len)]
            if v ~= nil then
                r = resolve_value(txn, v, ctx)
                if r then
                    -- len includes lr; subtract to get path-only specificity.
                    r.score = len - lr_len
                    return r
                end
            end
        end
    end

    idx = index_for(files.regex, now)
    if idx ~= nil then
        for _, k in ipairs(idx.sorted) do
            local re = get_regex(k)
            if re ~= nil and re:exec(blr) then
                r = resolve_value(txn, idx.values[k], ctx)
                if r then
                    r.score = SCORE_REGEX
                    return r
                end
            end
        end
    end
    return nil
end

-- Spec precedence after path specificity and host: method, header count,
-- query count. Equal ranks keep the first candidate considered (list order).
local function cand_better(a, b)
    if a.score ~= b.score then
        return a.score > b.score
    end
    if a.host ~= b.host then
        return a.host > b.host
    end
    if a.nm ~= b.nm then
        return a.nm
    end
    if a.nh ~= b.nh then
        return a.nh > b.nh
    end
    if a.nq ~= b.nq then
        return a.nq > b.nq
    end
    return false
end

-- find_route picks the most specific route across two candidate sources:
--   txn.selected_listener_route          → routes attached via exact hostname
--   txn.selected_listener_route_wildcard → routes attached via wildcard hostname
-- Each may be comma-separated. We score every candidate against the path maps;
-- path specificity is primary (exact > longest prefix > regex), exact-host
-- breaks ties over wildcard-host, then method, header count and query count
-- decide, per the Gateway API match precedence rules.
-- maps_dir: maps directory for this frontend (e.g. /usr/local/hug/maps/hug_http_80)
local HOST_EXACT, HOST_WILD = 2, 1

local function find_route(txn, maps_dir)
    local lr_exact = txn.f:var("txn.selected_listener_route") or ""
    local lr_wild  = txn.f:var("txn.selected_listener_route_wildcard") or ""
    local path     = txn.f:var("txn.path") or ""

    if lr_exact == "" and lr_wild == "" then return end

    local files = {
        exact  = maps_dir .. "/path_exact.map",
        prefix = maps_dir .. "/path_prefix.map",
        regex  = maps_dir .. "/path_regex.map",
    }

    local now = now_s()
    local ctx = {}
    local blr_parts = {}
    local best = nil

    local function consider(lr_str, host_rank)
        if lr_str == "" then return end
        for _, lr in ipairs(core.tokenize(lr_str, ",", true)) do
            local blr = lr .. path
            table.insert(blr_parts, blr)

            local r = resolve_passing(txn, files, blr, #lr, ctx, now)
            if r ~= nil then
                r.host = host_rank
                if best == nil or cand_better(r, best) then
                    best = r
                end
            end
        end
    end

    consider(lr_exact, HOST_EXACT)
    consider(lr_wild,  HOST_WILD)

    txn:set_var("txn.base_listener_route", table.concat(blr_parts, ","))
    if best ~= nil then
        txn:set_var("txn.route", best.target)
    end
end

core.register_action("find_route", { "http-req" }, find_route, 1)

-- select_route resolves a conditional map value ("~1;...") left in txn.route
-- by the native fast-path map converters, which cannot evaluate conditions.
-- Candidates are tried in order; when all fail, the remaining path maps
-- (shorter prefixes, then regex) are retried for the same listener-route,
-- mirroring the ifnotexists chain the fast path could not redo natively.
local function select_route(txn, maps_dir)
    local raw = txn.f:var("txn.route")
    if raw == nil or raw:sub(1, 2) ~= "~1" then
        return
    end

    local ctx = {}
    for _, cand in ipairs(parse_cond(raw)) do
        if eval_cands(txn, cand.conds, ctx) then
            txn:set_var("txn.route", cand.target)
            return
        end
    end

    local lr = txn.f:var("txn.lr")
    local path = txn.f:var("txn.path")
    if lr ~= nil and lr ~= "" and path ~= nil then
        local files = {
            exact  = maps_dir .. "/path_exact.map",
            prefix = maps_dir .. "/path_prefix.map",
            regex  = maps_dir .. "/path_regex.map",
        }
        local r = resolve_passing(txn, files, lr .. path, #lr, ctx, now_s())
        if r ~= nil then
            txn:set_var("txn.route", r.target)
            return
        end
    end

    -- No candidate matched: leave no route so the request 404s.
    txn:set_var("txn.route", nil)
end

core.register_action("select_route", { "http-req" }, select_route, 1)

-- Register a converter to reverse the host string (e.g. "www.example.com" becomes ".com.example.www")
core.register_converters("reverse_host", function(val)
    if val == nil or val == "" then
        return "."
    end

    local labels = {}
    -- Split the string by dots
    for label in string.gmatch(val, "[^.]+") do
        table.insert(labels, 1, label)
    end

    -- Join with dots and prefix with a leading dot
    -- Example: "bar.com" -> ".com.bar"
    return "." .. table.concat(labels, ".")
end)
