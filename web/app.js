import Alpine from "https://cdn.jsdelivr.net/npm/alpinejs@3.14.9/dist/module.esm.js";
import { dashboard } from "./app/dashboard.js";

Alpine.data("dashboard", dashboard);
window.Alpine = Alpine;
Alpine.start();
