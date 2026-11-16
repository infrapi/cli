-- serverless-pre-function of the openapi-stub route: an OpenAPI document
-- for any api at /apis/<api>/<version>/-/openapi.json, declaring one namespaced
-- resource, "widgets". infrapi-cli dev render puts it in the openapi-stub route.
return function(conf, ctx)
  local core = require("apisix.core")
  local api, version = ngx.var.uri:match("^/apis/([a-z0-9.-]+)/(v[0-9a-z]+)/%-/openapi%.json$")
  if not api then
    return core.response.exit(404, {status = 404, title = "Not Found"})
  end
  local op = {responses = {["200"] = {description = "ok"}}}
  local widgets = "/apis/" .. api .. "/" .. version .. "/namespaces/{namespace}/widgets"
  core.response.set_header("Content-Type", "application/json")
  return core.response.exit(200, {
    openapi = "3.0.3",
    info = {title = api .. " (stub)", version = version},
    paths = {
      [widgets] = {get = op, post = op},
      [widgets .. "/{name}"] = {get = op, delete = op},
    },
  })
end
