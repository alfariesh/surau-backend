import { HocuspocusProvider } from "@hocuspocus/provider";
const token = process.env.TOKEN;
const provider = new HocuspocusProvider({
  url: "ws://localhost:8090",
  name: "page:990001:1",
  token,
  onSynced() {
    setTimeout(() => {
      const text = provider.document.getXmlFragment("default").toString();
      console.log(text.includes(process.env.MARKER) ? "RESTORED_OK" : "RESTORE_FAILED: " + text.slice(0, 200));
      provider.destroy();
      process.exit(text.includes(process.env.MARKER) ? 0 : 1);
    }, 500);
  },
});
setTimeout(() => { console.log("TIMEOUT"); process.exit(1); }, 15000);
