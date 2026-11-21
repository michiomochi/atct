type Rgb = readonly [number, number, number];

export type Oklch = {
  l: number;
  c: number;
  h: number;
};

const clamp = (value: number, min: number, max: number): number =>
  Math.min(max, Math.max(min, value));

function linearToSrgb(value: number): number {
  const encoded =
    value <= 0.0031308
      ? 12.92 * value
      : 1.055 * Math.pow(value, 1 / 2.4) - 0.055;
  return Math.round(clamp(encoded, 0, 1) * 255);
}

export function oklchToSrgb(l: number, c: number, h: number): [number, number, number] {
  const radians = (h * Math.PI) / 180;
  const a = c * Math.cos(radians);
  const b = c * Math.sin(radians);

  const lComponent = l + 0.3963377774 * a + 0.2158037573 * b;
  const mComponent = l - 0.1055613458 * a - 0.0638541728 * b;
  const sComponent = l - 0.0894841775 * a - 1.291485548 * b;

  const lCube = lComponent ** 3;
  const mCube = mComponent ** 3;
  const sCube = sComponent ** 3;

  const red = 4.0767416621 * lCube - 3.3077115913 * mCube + 0.2309699292 * sCube;
  const green = -1.2684380046 * lCube + 2.6097574011 * mCube - 0.3413193965 * sCube;
  const blue = -0.0041960863 * lCube - 0.7034186147 * mCube + 1.707614701 * sCube;

  return [linearToSrgb(red), linearToSrgb(green), linearToSrgb(blue)];
}

export function mixOklchWithWhite(
  l: number,
  c: number,
  h: number,
  percent: number,
): [number, number, number] {
  const whiteWeight = percent / 100;
  const colorWeight = 1 - whiteWeight;
  return [l * colorWeight + whiteWeight, c * colorWeight, h];
}

function relativeLuminance(rgb: Rgb): number {
  const [red, green, blue] = rgb.map((channel) => {
    const normalized = channel / 255;
    return normalized <= 0.04045
      ? normalized / 12.92
      : ((normalized + 0.055) / 1.055) ** 2.4;
  });

  return 0.2126 * red + 0.7152 * green + 0.0722 * blue;
}

export function contrastRatio(rgbA: Rgb, rgbB: Rgb): number {
  const luminanceA = relativeLuminance(rgbA);
  const luminanceB = relativeLuminance(rgbB);
  const lighter = Math.max(luminanceA, luminanceB);
  const darker = Math.min(luminanceA, luminanceB);
  return (lighter + 0.05) / (darker + 0.05);
}

export function parseBrandFromCss(cssText: string): Oklch {
  const number = "[-+]?\\d*\\.?\\d+";
  const pattern = new RegExp(
    `--color-kumo-brand\\s*:\\s*light-dark\\(\\s*oklch\\(\\s*(${number})%\\s+(${number})\\s+(${number})(?:deg)?\\s*\\)\\s*,`,
  );
  const match = pattern.exec(cssText);

  if (match === null) {
    throw new Error("Could not find the first light-dark() brand color in global.css");
  }

  return {
    l: Number(match[1]) / 100,
    c: Number(match[2]),
    h: Number(match[3]),
  };
}
