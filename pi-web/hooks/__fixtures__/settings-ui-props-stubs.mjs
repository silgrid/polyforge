// Props-preserving stand-ins for components/SettingsUi's pieces, for a
// behaviour test that reads the rendered element tree on the hook shim (the
// real SettingsUi calls createContext at module load, which the shim lacks).
export const ConfigSwitch = (props) => ({ type: "switch", props });
export const ConfigButton = (props) => ({ type: "button", props });
export const ConfigDetailGrid = (props) => props.children ?? null;
export const ConfigDetailGridRow = (props) => ({ type: "row", props });