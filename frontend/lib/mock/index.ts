// Entry point of the demo-mode backend. Importing it registers every route.
import "./handlers/identity";
import "./handlers/leads";
import "./handlers/data";
import "./handlers/campaigns";
import "./handlers/analytics";
import "./handlers/deals";

export { mockFetch } from "./server";
export { resetDB } from "./db";
