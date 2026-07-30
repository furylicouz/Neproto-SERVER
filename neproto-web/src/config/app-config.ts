import packageJson from "../../package.json";

const currentYear = new Date().getFullYear();

export const APP_CONFIG = {
  name: "NeProto Admin",
  version: packageJson.version,
  copyright: `© ${currentYear}, NeProto Admin.`,
  meta: {
    title: "NeProto Admin",
    description: "NeProto administration dashboard.",
  },
};
