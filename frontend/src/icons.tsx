import { useId } from 'react';

const ANTIGRAVITY_PATH =
  'M89.6992 93.695C94.3659 97.195 101.366 94.8617 94.9492 88.445C75.6992 69.7783 79.7825 18.445 55.8659 18.445C31.9492 18.445 36.0325 69.7783 16.7825 88.445C9.78251 95.445 17.3658 97.195 22.0325 93.695C40.1159 81.445 38.9492 59.8617 55.8659 59.8617C72.7825 59.8617 71.6159 81.445 89.6992 93.695Z';

function AntigravityIcon({ size }: { size: number }) {
  const uid = useId();
  const id = (suffix: string) => `${uid}-${suffix}`;
  return (
    <svg width={size} height={size} viewBox="10 11 92 92" fill="none" aria-hidden="true">
      <path d={ANTIGRAVITY_PATH} fill="#3186FF" />
      <mask id={id('m')} style={{ maskType: 'alpha' }} maskUnits="userSpaceOnUse" x="13" y="18" width="85" height="78">
        <path d={ANTIGRAVITY_PATH} fill="black" />
      </mask>
      <g mask={`url(#${id('m')})`}>
        <g filter={`url(#${id('f0')})`}>
          <ellipse
            cx="22.7873"
            cy="26.8098"
            rx="22.7873"
            ry="26.8098"
            transform="matrix(-0.112784 0.99362 -0.99362 -0.112781 66.2473 -15.5344)"
            fill="#FFE432"
          />
        </g>
        <g filter={`url(#${id('f1')})`}>
          <ellipse cx="96.491" cy="35.1231" rx="29.5007" ry="30.1492" transform="rotate(76.9243 96.491 35.1231)" fill="#FC413D" />
        </g>
        <g filter={`url(#${id('f2')})`}>
          <ellipse cx="9.02988" cy="41.6647" rx="30.832" ry="39.9417" transform="rotate(74.1257 9.02988 41.6647)" fill="#00B95C" />
        </g>
        <g filter={`url(#${id('f3')})`}>
          <ellipse cx="11.2212" cy="42.8915" rx="30.22" ry="33.2695" transform="rotate(45.6065 11.2212 42.8915)" fill="#00B95C" />
        </g>
        <g filter={`url(#${id('f4')})`}>
          <ellipse cx="75.7546" cy="104.822" rx="29.0177" ry="27.943" transform="rotate(76.9243 75.7546 104.822)" fill="#3186FF" />
        </g>
        <g filter={`url(#${id('f5')})`}>
          <ellipse
            cx="33.5661"
            cy="35.4043"
            rx="33.5661"
            ry="35.4043"
            transform="matrix(-0.409539 0.912293 -0.912294 -0.409537 101.25 -15.1674)"
            fill="#FBBC04"
          />
        </g>
        <g filter={`url(#${id('f6')})`}>
          <path
            d="M2.56802 149.695C-15.8116 142.48 15.5987 83.1163 23.4093 63.2203C31.22 43.3244 52.4514 33.0447 70.831 40.26C89.2107 47.4753 110.996 87.2162 103.185 107.112C95.3742 127.008 20.9477 156.91 2.56802 149.695Z"
            fill="#3186FF"
          />
        </g>
        <g filter={`url(#${id('f7')})`}>
          <path
            d="M113.934 75.8079C109.013 81.5509 96.1724 78.6224 85.253 69.2667C74.3335 59.911 69.4704 47.6711 74.391 41.928C79.3116 36.185 92.1525 39.1136 103.072 48.4692C113.991 57.8249 118.855 70.0648 113.934 75.8079Z"
            fill="#749BFF"
          />
        </g>
        <g filter={`url(#${id('f8')})`}>
          <ellipse cx="92.611" cy="23.7962" rx="44.2411" ry="27.5016" transform="rotate(34.0763 92.611 23.7962)" fill="#FC413D" />
        </g>
        <g filter={`url(#${id('f9')})`}>
          <ellipse cx="23.4949" cy="29.5887" rx="23.7071" ry="13.7869" transform="rotate(112.516 23.4949 29.5887)" fill="#FFEE48" />
        </g>
      </g>
      <defs>
        <filter id={id('f0')} x="2.49348" y="-26.5423" width="69.0899" height="61.2525" filterUnits="userSpaceOnUse" colorInterpolationFilters="sRGB">
          <feGaussianBlur stdDeviation="3.89034" />
        </filter>
        <filter id={id('f1')} x="28.7524" y="-32.0333" width="135.477" height="134.313" filterUnits="userSpaceOnUse" colorInterpolationFilters="sRGB">
          <feGaussianBlur stdDeviation="18.8078" />
        </filter>
        <filter id={id('f2')} x="-62.2884" y="-21.9253" width="142.637" height="127.18" filterUnits="userSpaceOnUse" colorInterpolationFilters="sRGB">
          <feGaussianBlur stdDeviation="15.9884" />
        </filter>
        <filter id={id('f3')} x="-52.5697" y="-20.8346" width="127.582" height="127.452" filterUnits="userSpaceOnUse" colorInterpolationFilters="sRGB">
          <feGaussianBlur stdDeviation="15.9884" />
        </filter>
        <filter id={id('f4')} x="17.3619" y="45.4646" width="116.786" height="118.715" filterUnits="userSpaceOnUse" colorInterpolationFilters="sRGB">
          <feGaussianBlur stdDeviation="15.1937" />
        </filter>
        <filter id={id('f5')} x="-7.44765" y="-60.4737" width="125.303" height="122.858" filterUnits="userSpaceOnUse" colorInterpolationFilters="sRGB">
          <feGaussianBlur stdDeviation="13.7698" />
        </filter>
        <filter id={id('f6')} x="-27.7086" y="13.3597" width="157.119" height="162.029" filterUnits="userSpaceOnUse" colorInterpolationFilters="sRGB">
          <feGaussianBlur stdDeviation="12.297" />
        </filter>
        <filter id={id('f7')} x="50.4638" y="16.981" width="87.3973" height="83.7738" filterUnits="userSpaceOnUse" colorInterpolationFilters="sRGB">
          <feGaussianBlur stdDeviation="11.0036" />
        </filter>
        <filter id={id('f8')} x="34.2604" y="-28.457" width="116.701" height="104.506" filterUnits="userSpaceOnUse" colorInterpolationFilters="sRGB">
          <feGaussianBlur stdDeviation="9.29385" />
        </filter>
        <filter id={id('f9')} x="-15.1522" y="-15.9493" width="77.2941" height="91.076" filterUnits="userSpaceOnUse" colorInterpolationFilters="sRGB">
          <feGaussianBlur stdDeviation="11.5027" />
        </filter>
      </defs>
    </svg>
  );
}

export function ZheerLogo({ size = 24 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
      <path
        fill="currentColor"
        d="M3.5 10.1C3.08 8.67 3.05 6.17 3.52 4.52 3.81 3.47 4.24 3.04 4.8 3.05S5.88 3.39 6.48 3.93 8.12 5.66 8.59 6.47C10.18 5.85 12.38 5.71 14.1 6.07 15 5.18 15.52 4.7 16.49 4.89S19.35 5.7 20.46 6.4 21.35 7.17 21.25 7.83C21.1 8.9 20.87 9.88 20.57 10.66 21.02 11.68 21.2 12.7 21.17 13.96C21.15 18.16 18.3 20.91 11.96 20.93 6.34 20.97 2.7 18.39 2.72 13.82C2.72 12.37 3.04 11.15 3.5 10.1Z"
      />
      <path
        fill="none"
        stroke="var(--brand-cutout)"
        strokeWidth="1.25"
        strokeLinecap="round"
        strokeLinejoin="round"
        d="M8.2 11.65 10.87 13.25 8.2 14.85M12.66 13.75H15.8"
      />
      <path
        fill="none"
        stroke="var(--brand-cutout)"
        strokeWidth="1.05"
        strokeLinecap="round"
        strokeLinejoin="round"
        d="M15.65 6.22C15.97 7.1 16.36 8.33 16.68 8.91 17.03 9.63 17.64 9.92 18.26 9.63 18.84 9.37 19.48 8.86 20.03 8.38"
      />
    </svg>
  );
}

export function AgentIcon({ id, size = 22 }: { id: string; size?: number }) {
  if (id === 'codex') {
    return (
      <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" fillRule="evenodd" aria-hidden="true">
        <path d="M9.205 8.658v-2.26c0-.19.072-.333.238-.428l4.543-2.616c.619-.357 1.356-.523 2.117-.523 2.854 0 4.662 2.212 4.662 4.566 0 .167 0 .357-.024.547l-4.71-2.759a.797.797 0 00-.856 0l-5.97 3.473zm10.609 8.8V12.06c0-.333-.143-.57-.429-.737l-5.97-3.473 1.95-1.118a.433.433 0 01.476 0l4.543 2.617c1.309.76 2.189 2.378 2.189 3.948 0 1.808-1.07 3.473-2.76 4.163zM7.802 12.703l-1.95-1.142c-.167-.095-.239-.238-.239-.428V5.899c0-2.545 1.95-4.472 4.591-4.472 1 0 1.927.333 2.712.928L8.23 5.067c-.285.166-.428.404-.428.737v6.898zM12 15.128l-2.795-1.57v-3.33L12 8.658l2.795 1.57v3.33L12 15.128zm1.796 7.23c-1 0-1.927-.332-2.712-.927l4.686-2.712c.285-.166.428-.404.428-.737v-6.898l1.974 1.142c.167.095.238.238.238.428v5.233c0 2.545-1.974 4.472-4.614 4.472zm-5.637-5.303l-4.544-2.617c-1.308-.761-2.188-2.378-2.188-3.948A4.482 4.482 0 014.21 6.327v5.423c0 .333.143.571.428.738l5.947 3.449-1.95 1.118a.432.432 0 01-.476 0zm-.262 3.9c-2.688 0-4.662-2.021-4.662-4.519 0-.19.024-.38.047-.57l4.686 2.71c.286.167.571.167.856 0l5.97-3.448v2.26c0 .19-.07.333-.237.428l-4.543 2.616c-.619.357-1.356.523-2.117.523zm5.899 2.83a5.947 5.947 0 005.827-4.756C22.287 18.339 24 15.84 24 13.296c0-1.665-.713-3.282-1.998-4.448.119-.5.19-.999.19-1.498 0-3.401-2.759-5.947-5.946-5.947-.642 0-1.26.095-1.88.31A5.962 5.962 0 0010.205 0a5.947 5.947 0 00-5.827 4.757C1.713 5.447 0 7.945 0 10.49c0 1.666.713 3.283 1.998 4.448-.119.5-.19 1-.19 1.499 0 3.401 2.759 5.946 5.946 5.946.642 0 1.26-.095 1.88-.309a5.96 5.96 0 004.162 1.713z" />
      </svg>
    );
  }
  if (id === 'claude') {
    return (
      <svg width={size} height={size} viewBox="0 0 24 24" aria-hidden="true">
        <path
          fill="#D97757"
          d="M4.709 15.955l4.72-2.647.08-.23-.08-.128H9.2l-.79-.048-2.698-.073-2.339-.097-2.266-.122-.571-.121L0 11.784l.055-.352.48-.321.686.06 1.52.103 2.278.158 1.652.097 2.449.255h.389l.055-.157-.134-.098-.103-.097-2.358-1.596-2.552-1.688-1.336-.972-.724-.491-.364-.462-.158-1.008.656-.722.881.06.225.061.893.686 1.908 1.476 2.491 1.833.365.304.145-.103.019-.73-.164-.274-1.355-2.446-1.446-2.49-.644-1.032-.17-.619a2.97 2.97 0 01-.104-.729L6.283.134 6.696 0l.996.134.42.364.62 1.414 1.002 2.229 1.555 3.03.456.898.243.832.091.255h.158V9.01l.128-1.706.237-2.095.23-2.695.08-.76.376-.91.747-.492.584.28.48.685-.067.444-.286 1.851-.559 2.903-.364 1.942h.212l.243-.242.985-1.306 1.652-2.064.73-.82.85-.904.547-.431h1.033l.76 1.129-.34 1.166-1.064 1.347-.881 1.142-1.264 1.7-.79 1.36.073.11.188-.02 2.856-.606 1.543-.28 1.841-.315.833.388.091.395-.328.807-1.969.486-2.309.462-3.439.813-.042.03.049.061 1.549.146.662.036h1.622l3.02.225.79.522.474.638-.079.485-1.215.62-1.64-.389-3.829-.91-1.312-.329h-.182v.11l1.093 1.068 2.006 1.81 2.509 2.33.127.578-.322.455-.34-.049-2.205-1.657-.851-.747-1.926-1.62h-.128v.17l.444.649 2.345 3.521.122 1.08-.17.353-.608.213-.668-.122-1.374-1.925-1.415-2.167-1.143-1.943-.14.08-.674 7.254-.316.37-.729.28-.607-.461-.322-.747.322-1.476.389-1.924.315-1.53.286-1.9.17-.632-.012-.042-.14.018-1.434 1.967-2.18 2.945-1.726 1.845-.414.164-.717-.37.067-.662.401-.589 2.388-3.036 1.44-1.882.93-1.086-.006-.158h-.055L4.132 18.56l-1.13.146-.487-.456.061-.746.231-.243 1.908-1.312-.006.006z"
        />
      </svg>
    );
  }
  if (id === 'antigravity') {
    return <AntigravityIcon size={size} />;
  }
  if (id === 'pi') {
    return (
      <svg width={size} height={size} viewBox="145 145 510 510" aria-hidden="true">
        <path fill="#F09082" d="M165.29 165.29H517.36V400H400V282.65H165.29Z" />
        <path fill="#4D9ABF" d="M165.29 282.65H282.65V400H400V517.36H282.65V634.72H165.29Z" />
        <path fill="#F1BE58" d="M517.36 400H634.72V634.72H517.36Z" />
      </svg>
    );
  }
  if (id === 'orca') {
    return (
      <svg width={size} height={size} viewBox="0 0 24 24" fill="currentColor" aria-hidden="true">
        <path d="M14.11 5.56C14.04 5.54 13.97 5.62 13.91 5.82L11.74 13.48C11.46 14.22 10.16 14.92 8.70 15.20C7.18 15.48 5.98 14.92 4.30 14.74C3.30 14.64 2.48 15.32 1.84 16.22C1.74 16.36 1.84 16.43 2.12 16.43L8.35 16.35C9.68 16.28 10.64 16.06 11.28 15.76C11.38 15.71 11.44 15.60 11.50 15.40L14.20 5.86C14.24 5.68 14.19 5.58 14.11 5.56Z" />
        <path d="M22.14 8.36C20.22 8.16 18.08 8.12 16.68 8.74C15.10 9.44 14.08 10.92 13.40 13.06L11.74 18.28C11.68 18.46 11.82 18.52 11.94 18.32L14.84 13.38C15.54 12.18 16.12 11.60 17.28 11.14C19.00 10.46 20.90 9.64 22.16 8.54C22.25 8.46 22.23 8.37 22.14 8.36Z" />
        <ellipse cx="16.89" cy="7.29" rx="1.44" ry="0.52" transform="rotate(-9.3 16.89 7.29)" />
      </svg>
    );
  }
  if (id === 'api') {
    return (
      <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
        <path d="M10 13a5 5 0 0 0 7.54.54l3-3a5 5 0 0 0-7.07-7.07l-1.72 1.71" />
        <path d="M14 11a5 5 0 0 0-7.54-.54l-3 3a5 5 0 0 0 7.07 7.07l1.71-1.71" />
      </svg>
    );
  }
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.9" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <path d="M12 22v-5" />
      <path d="M9 8V2" />
      <path d="M15 8V2" />
      <path d="M18 8v5a4 4 0 0 1-4 4h-4a4 4 0 0 1-4-4V8Z" />
    </svg>
  );
}
