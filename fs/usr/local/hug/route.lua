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

-- Path maps are pre-loaded by HAProxy via dummy ACLs in haproxy.cfg, so
-- core.get_patref returns a handle to the live in-memory pat_ref. Iterating
-- with pairs() sees runtime-socket updates (set/add/del map) immediately.
local patref_cache = {}

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

local function exact_lookup(filepath, key)
    local r = get_ref(filepath)
    if not r then return nil end
    for k, v in pairs(r) do
        if k == key then return v end
    end
    return nil
end

-- Returns value and match length (0 if no match).
local function prefix_lookup(filepath, key)
    local r = get_ref(filepath)
    if not r then return nil, 0 end
    local best_v, best_len = nil, 0
    for k, v in pairs(r) do
        if #k > best_len and key:sub(1, #k) == k then
            best_v, best_len = v, #k
        end
    end
    return best_v, best_len
end

local function regex_lookup(filepath, key)
    local r = get_ref(filepath)
    if not r then return nil end
    for k, v in pairs(r) do
        local ok, m = pcall(string.match, key, k)
        if ok and m then return v end
    end
    return nil
end

-- Scoring constants: exact > any prefix length > regex > nothing.
local SCORE_EXACT = 1e9
local SCORE_REGEX = -1

-- find_route picks the most specific route across two candidate sources:
--   txn.selected_listener_route          → routes attached via exact hostname
--   txn.selected_listener_route_wildcard → routes attached via wildcard hostname
-- Each may be comma-separated. We score every candidate against the path maps;
-- path specificity is primary (exact > longest prefix > regex), exact-host
-- breaks ties over wildcard-host (Gateway API: same path, more specific host wins).
-- maps_dir: maps directory for this frontend (e.g. /usr/local/hug/maps/hug_http_80)
local HOST_EXACT, HOST_WILD = 2, 1

local function find_route(txn, maps_dir)
    local lr_exact = txn.f:var("txn.selected_listener_route") or ""
    local lr_wild  = txn.f:var("txn.selected_listener_route_wildcard") or ""
    local path     = txn.f:var("txn.path") or ""

    if lr_exact == "" and lr_wild == "" then return end

    local exact_file  = maps_dir .. "/path_exact.map"
    local prefix_file = maps_dir .. "/path_prefix.map"
    local regex_file  = maps_dir .. "/path_regex.map"

    local blr_parts  = {}
    local best_val   = nil
    local best_score = -1e9
    local best_host  = 0

    local function consider(lr_str, host_rank)
        if lr_str == "" then return end
        for _, lr in ipairs(core.tokenize(lr_str, ",", true)) do
            local blr = lr .. path
            table.insert(blr_parts, blr)

            local m, score
            m = exact_lookup(exact_file, blr)
            if m then
                score = SCORE_EXACT
            else
                local pv, plen = prefix_lookup(prefix_file, blr)
                if pv then
                    -- plen includes lr; subtract to get path-only specificity.
                    m, score = pv, plen - #lr
                else
                    local rv = regex_lookup(regex_file, blr)
                    if rv then m, score = rv, SCORE_REGEX end
                end
            end

            if m and (score > best_score or
                     (score == best_score and host_rank > best_host)) then
                best_val   = m
                best_score = score
                best_host  = host_rank
            end
        end
    end

    consider(lr_exact, HOST_EXACT)
    consider(lr_wild,  HOST_WILD)

    txn:set_var("txn.base_listener_route", table.concat(blr_parts, ","))
    if best_val ~= nil then
        txn:set_var("txn.route", best_val)
    end
end

core.register_action("find_route", { "http-req" }, find_route, 1)

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
