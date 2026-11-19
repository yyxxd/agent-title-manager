import { useState } from 'react';
import { AppearanceRow, type AppearanceControl } from './Appearance';
import { ModelSwitch } from './ModelSwitch';
import { rpc, type Preferences } from './rpc';
import { UpdateSection } from './Update';
import { Section, Status, useAction, useLoad } from './ui';

export function Settings({ appearance }: { appearance: AppearanceControl }) {
  const { value, error } = useLoad(() => rpc<Preferences>('desktop.get'), 'desktop');
  const [startup, setStartup] = useState<boolean>();
  const action = useAction();
  const quit = useAction();
  const enabled = startup ?? value?.startup ?? false;

  function toggle(next: boolean) {
    setStartup(next);
    void action.run(async () => {
      try {
        const persisted = await rpc<Preferences>('desktop.startup', { enabled: next });
        setStartup(persisted.startup);
      } catch (error) {
        setStartup(enabled);
        throw error;
      }
    }, { success: 'silent', failure: 'toast' });
  }

  return <div className="settings-page">
    <header className="page-head"><h1>设置</h1></header>
    <Section title="桌面偏好">
      <div className="settings-rows">
        <AppearanceRow {...appearance} />
        {value?.startupAvailable && <ModelSwitch checked={enabled} busy={action.busy} onChange={toggle}>开机启动</ModelSwitch>}
        <div className="setting-row"><span>后台运行</span><button type="button" disabled={quit.busy} onClick={() => void quit.run(() => rpc<void>('desktop.quit'), { success: 'silent', failure: 'toast' })}>退出桌面应用</button></div>
        <Status error={error ?? action.error ?? quit.error} />
      </div>
    </Section>
    <UpdateSection />
  </div>;
}
