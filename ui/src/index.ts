// The bundle's entry. The app imports this file and calls what it default
// exports, handing it the one object a module receives.
import { defineModule } from "@prusa/connect-ui/slots";

import AssistLine from "./assist-line.svelte";
import AssistSection from "./assist-section.svelte";
import { holdHost } from "./host";
import "./styles.css";

export default defineModule((host) => {
  holdHost(host);
  // Held to `contributes` in module.yaml.
  host.register.deviceSection("assist", AssistSection);
  host.register.deviceCard("assist-line", AssistLine);
});
