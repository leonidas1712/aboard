// Every scenario the lab offers, in the switcher's order; the first is the default.

import type { Scenario } from "../scenario";
import { busy } from "./busy";
import { empty } from "./empty";
import { solo } from "./solo";
import { team } from "./team";
import { workspace } from "./workspace";

export const scenarios: Scenario[] = [team, solo, empty, busy, workspace];
