-- vim: tabstop=4 shiftwidth=4 expandtab

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
