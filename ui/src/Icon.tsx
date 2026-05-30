import account_tree from "@material-symbols/svg-400/outlined/account_tree.svg?raw";
import chevron_right from "@material-symbols/svg-400/outlined/chevron_right.svg?raw";
import data_object from "@material-symbols/svg-400/outlined/data_object.svg?raw";
import delete_ from "@material-symbols/svg-400/outlined/delete.svg?raw";
import description from "@material-symbols/svg-400/outlined/description.svg?raw";
import explore from "@material-symbols/svg-400/outlined/explore.svg?raw";
import expand_more from "@material-symbols/svg-400/outlined/keyboard_arrow_down.svg?raw";
import filter_list from "@material-symbols/svg-400/outlined/filter_list.svg?raw";
import folder from "@material-symbols/svg-400/outlined/folder.svg?raw";
import folder_open from "@material-symbols/svg-400/outlined/folder_open.svg?raw";
import group from "@material-symbols/svg-400/outlined/group.svg?raw";
import info from "@material-symbols/svg-400/outlined/info.svg?raw";
import inventory_2 from "@material-symbols/svg-400/outlined/inventory_2.svg?raw";
import layers from "@material-symbols/svg-400/outlined/layers.svg?raw";
import lightbulb from "@material-symbols/svg-400/outlined/lightbulb.svg?raw";
import local_fire_department from "@material-symbols/svg-400/outlined/local_fire_department.svg?raw";
import settings from "@material-symbols/svg-400/outlined/settings.svg?raw";
import shield from "@material-symbols/svg-400/outlined/shield.svg?raw";
import trending_down from "@material-symbols/svg-400/outlined/trending_down.svg?raw";
import trending_up from "@material-symbols/svg-400/outlined/trending_up.svg?raw";
import vital_signs from "@material-symbols/svg-400/outlined/vital_signs.svg?raw";
import widgets from "@material-symbols/svg-400/outlined/widgets.svg?raw";

const ICONS: Record<string, string> = {
  account_tree,
  chevron_right,
  data_object,
  delete: delete_,
  description,
  explore,
  expand_more,
  filter_list,
  folder,
  folder_open,
  group,
  info,
  inventory_2,
  layers,
  lightbulb,
  local_fire_department,
  settings,
  shield,
  trending_down,
  trending_up,
  vital_signs,
  widgets,
};

export function Icon({ name, size, fill }: { name: string; size?: number; fill?: boolean }) {
  const svg = ICONS[name] ?? "";
  return (
    <span
      className={fill ? "micon micon-fill" : "micon"}
      aria-hidden
      style={{ fontSize: size ?? 18 }}
      dangerouslySetInnerHTML={{ __html: svg }}
    />
  );
}
